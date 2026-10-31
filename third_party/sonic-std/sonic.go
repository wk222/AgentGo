/*
 * Adapted from github.com/bytedance/sonic (compat.go, api.go, rawmessage.go).
 * Copyright 2021 ByteDance Inc.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

// Package sonic is a pure-Go, encoding/json based subset of
// github.com/bytedance/sonic that also builds on 32-bit targets.
package sonic

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"

	"github.com/bytedance/sonic/ast"
)

const (
	// UseStdJSON indicates the fallback implementation (encoding/json).
	UseStdJSON = iota
	// UseSonicJSON indicates the real sonic implementation (never in this module).
	UseSonicJSON
)

// APIKind is always UseStdJSON here.
const APIKind = UseStdJSON

// Config mirrors upstream sonic.Config. Only the options that encoding/json can
// honour have an effect (EscapeHTML, UseNumber, DisallowUnknownFields); the rest
// are accepted so existing struct literals keep compiling.
type Config struct {
	EscapeHTML              bool
	SortMapKeys             bool
	CompactMarshaler        bool
	NoQuoteTextMarshaler    bool
	NoNullSliceOrMap        bool
	UseInt64                bool
	UseNumber               bool
	UseUnicodeErrors        bool
	DisallowUnknownFields   bool
	CopyString              bool
	ValidateString          bool
	NoValidateJSONMarshaler bool
	NoValidateJSONSkip      bool
	NoEncoderNewline        bool
	EncodeNullForInfOrNan   bool
	CaseSensitive           bool
}

var (
	// ConfigDefault is the default config of the package-level helpers.
	ConfigDefault = Config{}.Froze()
	// ConfigStd is compatible with encoding/json.
	ConfigStd = Config{EscapeHTML: true, SortMapKeys: true, CompactMarshaler: true, CopyString: true, ValidateString: true}.Froze()
	// ConfigFastest is accepted for source compatibility.
	ConfigFastest = Config{NoValidateJSONMarshaler: true, NoValidateJSONSkip: true}.Froze()
)

// API is a binding of a specific config.
type API interface {
	MarshalToString(v interface{}) (string, error)
	Marshal(v interface{}) ([]byte, error)
	MarshalIndent(v interface{}, prefix, indent string) ([]byte, error)
	UnmarshalFromString(str string, v interface{}) error
	Unmarshal(data []byte, v interface{}) error
	NewEncoder(writer io.Writer) Encoder
	NewDecoder(reader io.Reader) Decoder
	Valid(data []byte) bool
}

// Encoder encodes JSON into an io.Writer.
type Encoder interface {
	Encode(val interface{}) error
	SetEscapeHTML(on bool)
	SetIndent(prefix, indent string)
}

// Decoder decodes JSON from an io.Reader.
type Decoder interface {
	Decode(val interface{}) error
	Buffered() io.Reader
	DisallowUnknownFields()
	More() bool
	UseNumber()
}

type frozenConfig struct{ Config }

// Froze converts the Config to an API.
func (cfg Config) Froze() API { return &frozenConfig{Config: cfg} }

func (cfg frozenConfig) marshalOptions(val interface{}, prefix, indent string) ([]byte, error) {
	w := bytes.NewBuffer([]byte{})
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(cfg.EscapeHTML)
	enc.SetIndent(prefix, indent)
	err := enc.Encode(val)
	out := w.Bytes()
	// json.Encoder always appends '\n', json.Marshal does not.
	if len(out) > 0 && out[len(out)-1] == '\n' {
		out = out[:len(out)-1]
	}
	return out, err
}

func (cfg frozenConfig) Marshal(val interface{}) ([]byte, error) {
	if !cfg.EscapeHTML {
		return cfg.marshalOptions(val, "", "")
	}
	return json.Marshal(val)
}

func (cfg frozenConfig) MarshalToString(val interface{}) (string, error) {
	out, err := cfg.Marshal(val)
	return string(out), err
}

func (cfg frozenConfig) MarshalIndent(val interface{}, prefix, indent string) ([]byte, error) {
	if !cfg.EscapeHTML {
		return cfg.marshalOptions(val, prefix, indent)
	}
	return json.MarshalIndent(val, prefix, indent)
}

func (cfg frozenConfig) UnmarshalFromString(buf string, val interface{}) error {
	dec := json.NewDecoder(bytes.NewBufferString(buf))
	if cfg.UseNumber {
		dec.UseNumber()
	}
	if cfg.DisallowUnknownFields {
		dec.DisallowUnknownFields()
	}
	if err := dec.Decode(val); err != nil {
		return err
	}
	// reject trailing characters, like upstream
	offset := dec.InputOffset()
	if t, err := dec.Token(); !(t == nil && err == io.EOF) {
		return &json.SyntaxError{Offset: offset}
	}
	return nil
}

func (cfg frozenConfig) Unmarshal(buf []byte, val interface{}) error {
	return cfg.UnmarshalFromString(string(buf), val)
}

func (cfg frozenConfig) NewEncoder(writer io.Writer) Encoder {
	enc := json.NewEncoder(writer)
	if !cfg.EscapeHTML {
		enc.SetEscapeHTML(cfg.EscapeHTML)
	}
	return enc
}

func (cfg frozenConfig) NewDecoder(reader io.Reader) Decoder {
	dec := json.NewDecoder(reader)
	if cfg.UseNumber {
		dec.UseNumber()
	}
	if cfg.DisallowUnknownFields {
		dec.DisallowUnknownFields()
	}
	return dec
}

func (cfg frozenConfig) Valid(data []byte) bool { return json.Valid(data) }

// Marshal returns the JSON encoding bytes of v.
func Marshal(val interface{}) ([]byte, error) { return ConfigDefault.Marshal(val) }

// MarshalIndent is like Marshal but applies Indent to format the output.
func MarshalIndent(v interface{}, prefix, indent string) ([]byte, error) {
	return ConfigDefault.MarshalIndent(v, prefix, indent)
}

// MarshalString returns the JSON encoding string of v.
func MarshalString(val interface{}) (string, error) { return ConfigDefault.MarshalToString(val) }

// Unmarshal parses the JSON-encoded data and stores the result in the value pointed to by v.
func Unmarshal(buf []byte, val interface{}) error { return ConfigDefault.Unmarshal(buf, val) }

// UnmarshalString is like Unmarshal, except buf is a string.
func UnmarshalString(buf string, val interface{}) error {
	return ConfigDefault.UnmarshalFromString(buf, val)
}

// Valid reports whether data is a valid JSON encoding.
func Valid(data []byte) bool { return ConfigDefault.Valid(data) }

// Get locates path in src and returns the partial JSON as an ast.Node.
// Each path element is an int (array index) or a string (object key).
func Get(src []byte, path ...interface{}) (ast.Node, error) {
	return ast.GetFromString(string(src), path...)
}

// GetFromString is Get for a string source.
func GetFromString(src string, path ...interface{}) (ast.Node, error) {
	return ast.GetFromString(src, path...)
}

// NoCopyRawMessage is a raw encoded JSON value that is referenced, not copied.
type NoCopyRawMessage []byte

// MarshalJSON returns m as the JSON encoding of m.
func (m NoCopyRawMessage) MarshalJSON() ([]byte, error) {
	if m == nil {
		return []byte("null"), nil
	}
	return m, nil
}

// UnmarshalJSON sets *m to a reference of data.
func (m *NoCopyRawMessage) UnmarshalJSON(data []byte) error {
	if m == nil {
		return errors.New("sonic.NoCopyRawMessage: UnmarshalJSON on nil pointer")
	}
	*m = data
	return nil
}
