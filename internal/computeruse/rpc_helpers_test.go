package computeruse

import (
	"encoding/json"
	"io"
)

func newDec(r io.Reader) *json.Decoder { return json.NewDecoder(r) }
func newEnc(w io.Writer) *json.Encoder { return json.NewEncoder(w) }
