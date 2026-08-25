package resolve

import "testing"

func TestValidateCertname(t *testing.T) {
	tests := []struct {
		certname string
		wantErr  bool
	}{
		{"web-01.example.test", false},
		{"", true},
		{"web/01.example.test", true},
		{`web\01.example.test`, true},
		{"web-01.example.test\x00", true},
		{"web-01..example.test", true},
		{"..", true},
	}
	for _, tc := range tests {
		t.Run(tc.certname, func(t *testing.T) {
			err := validateCertname(tc.certname)
			if (err != nil) != tc.wantErr {
				t.Errorf("validateCertname(%q) error = %v, wantErr %v", tc.certname, err, tc.wantErr)
			}
		})
	}
}

func TestValidateGlobSyntax(t *testing.T) {
	tests := []struct {
		pattern string
		wantErr bool
	}{
		{"*", false},
		{"/var/cache/*", false},
		{"exact-title", false},
		{"[unterminated", true},
	}
	for _, tc := range tests {
		t.Run(tc.pattern, func(t *testing.T) {
			err := validateGlobSyntax(tc.pattern)
			if (err != nil) != tc.wantErr {
				t.Errorf("validateGlobSyntax(%q) error = %v, wantErr %v", tc.pattern, err, tc.wantErr)
			}
		})
	}
}

func TestResolveFilePath(t *testing.T) {
	tests := []struct {
		name     string
		raw      string
		dir      string
		certname string
		want     string
		wantErr  bool
	}{
		{
			name:     "template as entire final component",
			raw:      "snapshots/catalogs/{certname}.json",
			dir:      "/config/dir",
			certname: "web-01.example.test",
			want:     "/config/dir/snapshots/catalogs/web-01.example.test.json",
		},
		{
			name:     "template not entire component is rejected",
			raw:      "snapshots/{certname}-catalog.json",
			dir:      "/config/dir",
			certname: "web-01.example.test",
			wantErr:  true,
		},
		{
			name:     "template with extension suffix is allowed",
			raw:      "snapshots/{certname}.json",
			dir:      "/config/dir",
			certname: "web-01.example.test",
			want:     "/config/dir/snapshots/web-01.example.test.json",
		},
		{
			name:     "template with prefix text is rejected",
			raw:      "snapshots/prefix-{certname}.json",
			dir:      "/config/dir",
			certname: "web-01.example.test",
			wantErr:  true,
		},
		{
			name:     "parent dir traversal rejected",
			raw:      "../outside/{certname}.json",
			dir:      "/config/dir",
			certname: "web-01.example.test",
			wantErr:  true,
		},
		{
			name:     "explicit absolute path allowed even though it is 'outside'",
			raw:      "/var/piace/snapshots/{certname}.json",
			dir:      "/config/dir",
			certname: "web-01.example.test",
			want:     "/var/piace/snapshots/web-01.example.test.json",
		},
		{
			name:     "no template is fine",
			raw:      "snapshots/shared.json",
			dir:      "/config/dir",
			certname: "web-01.example.test",
			want:     "/config/dir/snapshots/shared.json",
		},
		{
			name:     "empty raw resolves to empty",
			raw:      "",
			dir:      "/config/dir",
			certname: "web-01.example.test",
			want:     "",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := resolveFilePath(tc.raw, tc.dir, tc.certname)
			if (err != nil) != tc.wantErr {
				t.Fatalf("resolveFilePath(%q) error = %v, wantErr %v", tc.raw, err, tc.wantErr)
			}
			if err == nil && got != tc.want {
				t.Errorf("resolveFilePath(%q) = %q, want %q", tc.raw, got, tc.want)
			}
		})
	}
}

func TestValidateHTTPSEndpoint(t *testing.T) {
	tests := []struct {
		endpoint string
		wantErr  bool
	}{
		{"https://compiler.example.test:8140", false},
		{"http://compiler.example.test:8140", true},
		{"", true},
		{"not a url", true},
		{"https://", true},
	}
	for _, tc := range tests {
		t.Run(tc.endpoint, func(t *testing.T) {
			_, err := validateHTTPSEndpoint(tc.endpoint)
			if (err != nil) != tc.wantErr {
				t.Errorf("validateHTTPSEndpoint(%q) error = %v, wantErr %v", tc.endpoint, err, tc.wantErr)
			}
		})
	}
}

func TestValidateTLSPath(t *testing.T) {
	tests := []struct {
		value   string
		wantErr bool
	}{
		{"/etc/piace/ca.pem", false},
		{"", true},
		{"bad\x00path", true},
	}
	for _, tc := range tests {
		t.Run(tc.value, func(t *testing.T) {
			err := validateTLSPath("ca_bundle", tc.value)
			if (err != nil) != tc.wantErr {
				t.Errorf("validateTLSPath(%q) error = %v, wantErr %v", tc.value, err, tc.wantErr)
			}
		})
	}
}
