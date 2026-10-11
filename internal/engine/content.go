package engine

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// BlockType is the kind of a ContentBlock (protocol v1, docs/kernel-plugin-consensus.md §4).
//
// Compatibility: values are only ever added, never renamed or removed, and a
// consumer must ignore block types it does not know.
type BlockType string

const (
	BlockText       BlockType = "text"
	BlockImage      BlockType = "image"
	BlockAudio      BlockType = "audio"
	BlockFile       BlockType = "file"
	BlockCard       BlockType = "card" // structured data rendered by the host, never plugin UI code
	BlockToolCall   BlockType = "tool_call"
	BlockToolResult BlockType = "tool_result"
	BlockThinking   BlockType = "thinking"
)

// MediaRef points at media by reference. Large payloads (images, audio, files)
// must never travel inline as base64: put the bytes in a file or blob store and
// pass the reference.
type MediaRef struct {
	URI        string `json:"uri,omitempty"`  // file:///... or a runtime-local path
	Hash       string `json:"hash,omitempty"` // content hash, e.g. sha256:...
	MIME       string `json:"mime,omitempty"`
	Name       string `json:"name,omitempty"`
	Size       int64  `json:"size,omitempty"`
	Width      int    `json:"width,omitempty"`
	Height     int    `json:"height,omitempty"`
	DurationMs int64  `json:"duration_ms,omitempty"`
	Page       int    `json:"page,omitempty"` // 1-based page for paged documents (citations)
}

// ContentBlock is one piece of message content. It is the unit every channel,
// modality and UI exchanges with the kernel.
type ContentBlock struct {
	Type  BlockType       `json:"type"`
	Text  string          `json:"text,omitempty"`
	Media *MediaRef       `json:"media,omitempty"`
	Data  json.RawMessage `json:"data,omitempty"` // card payload / structured tool args or results
}

// MaxInlineBlockBytes bounds Text and Data so a block stays a message, not a file.
const MaxInlineBlockBytes = 256 << 10

// TextBlock, ImageBlock and friends are small constructors for the common cases.
func TextBlock(s string) ContentBlock { return ContentBlock{Type: BlockText, Text: s} }

func ImageBlock(uri string) ContentBlock {
	return ContentBlock{Type: BlockImage, Media: &MediaRef{URI: uri}}
}

func FileBlock(uri, name, mime string) ContentBlock {
	return ContentBlock{Type: BlockFile, Media: &MediaRef{URI: uri, Name: name, MIME: mime}}
}

// CardBlock wraps structured data for the host to render. kind names the card
// schema ("citations", ...); data is any JSON-marshalable value.
func CardBlock(kind string, data any) (ContentBlock, error) {
	raw, err := json.Marshal(map[string]any{"kind": kind, "data": data})
	if err != nil {
		return ContentBlock{}, err
	}
	return ContentBlock{Type: BlockCard, Data: raw}, nil
}

// Validate checks a block against the protocol. Unknown types are NOT an error:
// forward compatibility requires carrying them through untouched.
func (b ContentBlock) Validate() error {
	if strings.TrimSpace(string(b.Type)) == "" {
		return errors.New("content block: type is required")
	}
	if len(b.Text) > MaxInlineBlockBytes || len(b.Data) > MaxInlineBlockBytes {
		return fmt.Errorf("content block %q: inline text/data exceeds %d bytes; pass a media reference", b.Type, MaxInlineBlockBytes)
	}
	if len(b.Data) > 0 && !json.Valid(b.Data) {
		return fmt.Errorf("content block %q: data is not valid JSON", b.Type)
	}
	switch b.Type {
	case BlockImage, BlockAudio, BlockFile:
		if b.Media == nil || (b.Media.URI == "" && b.Media.Hash == "") {
			return fmt.Errorf("content block %q: media with uri or hash is required", b.Type)
		}
		if strings.HasPrefix(b.Media.URI, "data:") {
			return fmt.Errorf("content block %q: inline data: URIs are not allowed; pass a reference", b.Type)
		}
	case BlockCard:
		if len(b.Data) == 0 {
			return errors.New("content block \"card\": data is required")
		}
	}
	return nil
}

// ValidateBlocks validates every block and names the first bad one.
func ValidateBlocks(blocks []ContentBlock) error {
	for i, b := range blocks {
		if err := b.Validate(); err != nil {
			return fmt.Errorf("blocks[%d]: %w", i, err)
		}
	}
	return nil
}

// NormalizeRequest makes Blocks and the legacy Input/Images agree, so block-aware
// and legacy engines can both consume the same request.
//
//   - Blocks set: they are authoritative; Input (when empty) becomes the joined
//     text blocks and Images (when empty) the image block URIs.
//   - Blocks empty: they are built from Input (one text block) and Images.
func NormalizeRequest(req RunRequest) RunRequest {
	if len(req.Blocks) == 0 {
		if strings.TrimSpace(req.Input) != "" {
			req.Blocks = append(req.Blocks, TextBlock(req.Input))
		}
		for _, img := range req.Images {
			if img != "" {
				req.Blocks = append(req.Blocks, ImageBlock(img))
			}
		}
		return req
	}
	if req.Input == "" {
		req.Input = BlocksText(req.Blocks)
	}
	if len(req.Images) == 0 {
		for _, b := range req.Blocks {
			if b.Type == BlockImage && b.Media != nil && b.Media.URI != "" {
				req.Images = append(req.Images, b.Media.URI)
			}
		}
	}
	return req
}

// BlocksText joins the text of all text blocks (the legacy single-string view).
func BlocksText(blocks []ContentBlock) string {
	var parts []string
	for _, b := range blocks {
		if b.Type == BlockText && b.Text != "" {
			parts = append(parts, b.Text)
		}
	}
	return strings.Join(parts, "\n")
}

// EmptyToolOutput is what an empty tool result becomes. An empty tool message is
// dropped by downstream model APIs, which leaves the assistant's tool call
// unanswered and the next request fails with HTTP 400. Normalizing it here, once,
// means no engine has to remember to.
const EmptyToolOutput = "(no output)"

// NormalizeToolOutput returns EmptyToolOutput for blank output and reports
// whether it substituted.
func NormalizeToolOutput(out string) (string, bool) {
	if strings.TrimSpace(out) == "" {
		return EmptyToolOutput, true
	}
	return out, false
}
