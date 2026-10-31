package evaluation

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"agentgo/internal/textenc"
)

// CorpusDoc is one document of a retrieval suite.
type CorpusDoc struct {
	ID   string `json:"id"`
	Text string `json:"text"`
}

// RetrievalSuite is a corpus and the questions asked of it. The corpus is either written into the
// file (docs) or read from a folder (docs_dir); a suite that points at a real folder of the
// company's own documents is the useful kind, because it measures the documents that will
// actually be searched.
//
//	{
//	  "name": "expense-policy",
//	  "docs_dir": "docs",
//	  "cases": [
//	    {"query": "How do I get a taxi ride refunded?", "relevant": ["travel/taxi.md"]},
//	    {"id": "invoice", "query": "which invoices can be reimbursed", "relevant": ["finance/invoice.txt", "finance/faq.txt"]}
//	  ]
//	}
//
// Document IDs are the paths relative to docs_dir, with forward slashes. Only .md, .markdown and
// .txt files are read, and a file may be in UTF-8, UTF-16 or GBK.
type RetrievalSuite struct {
	Name    string          `json:"name,omitempty"`
	DocsDir string          `json:"docs_dir,omitempty"`
	Docs    []CorpusDoc     `json:"docs,omitempty"`
	Cases   []RetrievalCase `json:"cases"`
}

// LoadRetrievalSuite reads and validates a suite. A docs_dir is resolved relative to the suite
// file and read into Docs, so a loaded suite always carries its corpus.
func LoadRetrievalSuite(path string) (*RetrievalSuite, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	// The suite itself may have been saved by Notepad as GBK or with a BOM; JSON parsers accept neither.
	text, _ := textenc.Decode(raw)
	dec := json.NewDecoder(strings.NewReader(text))
	dec.DisallowUnknownFields() // a misspelt "relevant" would otherwise silently score every query zero
	var s RetrievalSuite
	if err := dec.Decode(&s); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if s.Name == "" {
		s.Name = strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	}
	if s.DocsDir != "" {
		if len(s.Docs) > 0 {
			return nil, fmt.Errorf("%s: use either docs or docs_dir, not both", path)
		}
		dir := s.DocsDir
		if !filepath.IsAbs(dir) {
			dir = filepath.Join(filepath.Dir(path), dir)
		}
		if s.Docs, err = LoadCorpusDir(dir); err != nil {
			return nil, fmt.Errorf("%s: docs_dir: %w", path, err)
		}
	}
	if err := s.Validate(); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &s, nil
}

// LoadCorpusDir reads every .md, .markdown and .txt file under dir, in path order.
func LoadCorpusDir(dir string) ([]CorpusDoc, error) {
	var docs []CorpusDoc
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		switch strings.ToLower(filepath.Ext(p)) {
		case ".md", ".markdown", ".txt":
		default:
			return nil
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		text, enc := textenc.Decode(data)
		rel, _ := filepath.Rel(dir, p)
		rel = filepath.ToSlash(rel)
		if enc == "unknown" {
			return fmt.Errorf("%s looks like a binary file, not text", rel)
		}
		if strings.TrimSpace(text) == "" {
			return nil
		}
		docs = append(docs, CorpusDoc{ID: rel, Text: text})
		return nil
	})
	if err != nil {
		return nil, err
	}
	if len(docs) == 0 {
		return nil, fmt.Errorf("no .md, .markdown or .txt documents under %s", dir)
	}
	sort.Slice(docs, func(i, j int) bool { return docs[i].ID < docs[j].ID })
	return docs, nil
}

// Validate checks the suite and gives unnamed cases the IDs q1, q2, ...
func (s *RetrievalSuite) Validate() error {
	if len(s.Docs) == 0 {
		return fmt.Errorf("the suite has no documents (set docs or docs_dir)")
	}
	known := make(map[string]bool, len(s.Docs))
	for i, d := range s.Docs {
		if strings.TrimSpace(d.ID) == "" {
			return fmt.Errorf("document %d has no id", i+1)
		}
		if known[d.ID] {
			return fmt.Errorf("duplicate document id %q", d.ID)
		}
		if strings.TrimSpace(d.Text) == "" {
			return fmt.Errorf("document %q is empty", d.ID)
		}
		known[d.ID] = true
	}
	if len(s.Cases) == 0 {
		return fmt.Errorf("the suite has no cases")
	}
	seen := make(map[string]bool, len(s.Cases))
	for i := range s.Cases {
		c := &s.Cases[i]
		if c.ID == "" {
			c.ID = fmt.Sprintf("q%d", i+1)
		}
		if seen[c.ID] {
			return fmt.Errorf("duplicate case id %q", c.ID)
		}
		seen[c.ID] = true
		if strings.TrimSpace(c.Query) == "" {
			return fmt.Errorf("case %s has an empty query", c.ID)
		}
		if len(c.Relevant) == 0 {
			return fmt.Errorf("case %s lists no relevant document", c.ID)
		}
		dup := make(map[string]bool, len(c.Relevant))
		for _, id := range c.Relevant {
			if !known[id] {
				if slashed := strings.ReplaceAll(id, `\`, "/"); known[slashed] {
					return fmt.Errorf("case %s: relevant document %q is not in the corpus; document IDs use forward slashes, did you mean %q?", c.ID, id, slashed)
				}
				return fmt.Errorf("case %s: relevant document %q is not in the corpus", c.ID, id)
			}
			if dup[id] {
				return fmt.Errorf("case %s lists %q twice", c.ID, id)
			}
			dup[id] = true
		}
	}
	return nil
}
