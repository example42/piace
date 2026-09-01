package snapshot

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/big"
	"sort"
	"strconv"
)

// CanonicalJSON encodes payload as compact canonical JSON: map keys sort
// lexicographically by UTF-8 bytes, arrays retain order, strings use
// standard JSON escaping, and numeric tokens normalize to their exact
// base-10 numeric value rather than a machine floating point
// approximation. That is what makes results deterministic: the same
// logical payload always produces byte-identical output regardless of
// map key insertion order or how a number was originally spelled ("1.50"
// vs "1.5", "1e2" vs "100").
//
// Accepted input shapes:
//
//   - nil, bool, string
//   - json.Number (preserves exact decimal digits, see
//     canonicalNumberString)
//   - float64, int, int64 (accepted for caller convenience when a value
//     was decoded/constructed without json.Number; float64 in particular
//     can only be as precise as whatever produced it, so a caller that
//     needs exact large-integer precision must supply json.Number or
//     json.RawMessage instead. See the package doc for why Checksum
//     always decodes with json.Decoder.UseNumber() rather than plain
//     json.Unmarshal)
//   - []any (recursively canonicalized, order preserved)
//   - map[string]any (recursively canonicalized, keys sorted)
//   - json.RawMessage (decoded with number precision preserved, then
//     canonicalized recursively)
//
// Any other input type is a programming error in the caller and returns
// an error rather than guessing at an encoding.
func CanonicalJSON(payload any) ([]byte, error) {
	var buf bytes.Buffer
	if err := encodeCanonical(&buf, payload); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// decodeAny decodes raw as one generic JSON value using json.Number for
// every numeric token (via json.Decoder.UseNumber()), never plain
// float64, so no numeric precision is lost before CanonicalJSON gets a
// chance to normalize it exactly. It also rejects trailing content after
// the first JSON value, since a snapshot payload must be exactly one JSON
// document.
func decodeAny(raw []byte) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, fmt.Errorf("decoding JSON value: %w", err)
	}
	if dec.More() {
		return nil, fmt.Errorf("trailing data after JSON value")
	}
	return v, nil
}

func encodeCanonical(buf *bytes.Buffer, v any) error {
	switch val := v.(type) {
	case nil:
		buf.WriteString("null")
		return nil
	case bool:
		if val {
			buf.WriteString("true")
		} else {
			buf.WriteString("false")
		}
		return nil
	case json.Number:
		s, err := canonicalNumberString(string(val))
		if err != nil {
			return err
		}
		buf.WriteString(s)
		return nil
	case float64:
		s, err := canonicalNumberString(strconv.FormatFloat(val, 'g', -1, 64))
		if err != nil {
			return err
		}
		buf.WriteString(s)
		return nil
	case int:
		buf.WriteString(strconv.Itoa(val))
		return nil
	case int64:
		buf.WriteString(strconv.FormatInt(val, 10))
		return nil
	case string:
		return encodeCanonicalString(buf, val)
	case []any:
		buf.WriteByte('[')
		for i, e := range val {
			if i > 0 {
				buf.WriteByte(',')
			}
			if err := encodeCanonical(buf, e); err != nil {
				return err
			}
		}
		buf.WriteByte(']')
		return nil
	case map[string]any:
		keys := make([]string, 0, len(val))
		for k := range val {
			keys = append(keys, k)
		}
		// sort.Strings compares Go strings byte-by-byte, which is exactly UTF-8
		// byte order for valid UTF-8 strings, and UTF-8 byte order is what
		// canonical map key ordering means here.
		sort.Strings(keys)
		buf.WriteByte('{')
		for i, k := range keys {
			if i > 0 {
				buf.WriteByte(',')
			}
			if err := encodeCanonicalString(buf, k); err != nil {
				return err
			}
			buf.WriteByte(':')
			if err := encodeCanonical(buf, val[k]); err != nil {
				return err
			}
		}
		buf.WriteByte('}')
		return nil
	case json.RawMessage:
		decoded, err := decodeAny(val)
		if err != nil {
			return err
		}
		return encodeCanonical(buf, decoded)
	default:
		return fmt.Errorf("snapshot: canonical json: unsupported value type %T", v)
	}
}

// encodeCanonicalString writes s as a standard-escaped JSON string.
// encoding/json's Marshal for a string already produces spec-compliant
// JSON string escaping deterministically for a given input, which is all
// canonical requires here.
func encodeCanonicalString(buf *bytes.Buffer, s string) error {
	b, err := json.Marshal(s)
	if err != nil {
		return fmt.Errorf("encoding string: %w", err)
	}
	buf.Write(b)
	return nil
}

// canonicalNumberString normalizes a JSON numeric token s (e.g. "1.50",
// "1e2", "-0", "0.500") to its exact base-10 numeric value using
// arbitrary-precision rational arithmetic, never float64. This guarantees
// two differently-spelled encodings of the same value produce identical
// canonical bytes (e.g. "1.50" and "1.5" both normalize to "1.5"; "1e2"
// and "100" both normalize to "100"), and that a large integer such as a
// 19-digit factset/catalog hash-adjacent numeric field keeps every digit
// exactly, which float64 (53 bits of mantissa) cannot.
//
// Algorithm: parse s as an exact big.Rat (big.Rat.SetString accepts
// signed decimal-with-optional-exponent syntax, i.e. exactly JSON's
// number grammar). Every such value's reduced denominator can only have
// prime factors 2 and 5 (it came from a decimal literal), so it is always
// expressible as an exact finite decimal: multiply numerator and
// denominator by whichever additional powers of 2 and 5 are needed to
// make the denominator a power of 10, then format the scaled numerator
// with the decimal point inserted at that fixed offset and trailing
// fractional zeros trimmed. "-0" and any all-zero value normalize to "0"
// (there is no negative zero in exact decimal value terms).
func canonicalNumberString(s string) (string, error) {
	r := new(big.Rat)
	if _, ok := r.SetString(s); !ok {
		return "", fmt.Errorf("snapshot: invalid numeric token %q", s)
	}

	neg := r.Sign() < 0
	if neg {
		r.Neg(r)
	}

	num := new(big.Int).Set(r.Num())
	den := new(big.Int).Set(r.Denom())

	two := big.NewInt(2)
	five := big.NewInt(5)

	p, q := 0, 0
	rem := new(big.Int)
	for {
		rem.Mod(den, two)
		if rem.Sign() != 0 {
			break
		}
		den.Div(den, two)
		p++
	}
	for {
		rem.Mod(den, five)
		if rem.Sign() != 0 {
			break
		}
		den.Div(den, five)
		q++
	}
	if den.Cmp(big.NewInt(1)) != 0 {
		// Cannot happen for a value parsed from valid decimal JSON number
		// syntax; guarded defensively rather than assumed.
		return "", fmt.Errorf("snapshot: numeric token %q is not exactly representable in decimal", s)
	}

	// den (after the trial-division loops above) is now the part of the
	// original denominator with all factors of 2 and 5 removed, and must
	// be 1 for any value parseable as a JSON decimal literal (checked
	// above). The original denominator was therefore exactly 2^p * 5^q;
	// scaling num/den's numerator and denominator by 2^(n-p) * 5^(n-q)
	// (n = max(p, q)) brings the denominator to exactly 10^n.
	n := p
	if q > n {
		n = q
	}
	scale := new(big.Int).Mul(
		new(big.Int).Exp(two, big.NewInt(int64(n-p)), nil),
		new(big.Int).Exp(five, big.NewInt(int64(n-q)), nil),
	)
	digits := new(big.Int).Mul(num, scale).String()

	if n == 0 {
		if neg && digits != "0" {
			return "-" + digits, nil
		}
		return digits, nil
	}

	for len(digits) <= n {
		digits = "0" + digits
	}
	intPart := digits[:len(digits)-n]
	fracPart := trimTrailingZeros(digits[len(digits)-n:])

	result := intPart
	if fracPart != "" {
		result += "." + fracPart
	}
	if neg && result != "0" {
		result = "-" + result
	}
	return result, nil
}

// CanonicalNumberString exposes canonicalNumberString for reuse outside
// this package. The catalog normalizer (internal/normalize) needs the
// exact same base-10 canonical decimal normalization this package
// already uses for snapshot payload checksums, so that there is exactly
// one canonicalization behavior for numeric values across the codebase.
// Determinism requires it, and a second implementation would sooner or
// later be a second answer.
func CanonicalNumberString(raw string) (string, error) {
	return canonicalNumberString(raw)
}

func trimTrailingZeros(s string) string {
	i := len(s)
	for i > 0 && s[i-1] == '0' {
		i--
	}
	return s[:i]
}
