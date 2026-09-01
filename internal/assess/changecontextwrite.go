package assess

import (
	"fmt"
	"io"

	"gopkg.in/yaml.v3"
)

// EncodeChangeContext writes cc to w as a version-1 change context file,
// the document `piace explain --change` reads.
//
// It marshals through the same changeWire LoadChangeContext decodes, so
// what it produces is a document that decoder accepts by construction. A
// field renamed on one side cannot quietly stop round-tripping, which is
// the failure a generator living outside this package invites.
//
// No cap is applied here. Bounding caller-supplied free text belongs to
// the reader, which has to do it for every change context whatever
// produced it, including one written by hand.
func EncodeChangeContext(w io.Writer, cc ChangeContext) error {
	version := ChangeContextFileVersion
	doc := changeContextFile{
		Version: &version,
		Change: changeWire{
			BaseRef:      cc.BaseRef,
			HeadRef:      cc.HeadRef,
			Commits:      cc.Commits,
			ChangedPaths: cc.ChangedPaths,
			Title:        cc.Title,
			Description:  cc.Description,
		},
	}

	enc := yaml.NewEncoder(w)
	enc.SetIndent(2)
	if err := enc.Encode(doc); err != nil {
		return fmt.Errorf("encoding change context: %w", err)
	}
	return enc.Close()
}
