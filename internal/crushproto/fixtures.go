package crushproto

import (
	"embed"
	"encoding/json"
	"fmt"
)

//go:embed testdata/fixtures/*.json
var fixtureFS embed.FS

// fixture is a recorded GET response ({"status":200,"body":...}) captured from
// a real Crush server by scripts/crushproto/capture_fixtures.py.
type fixture struct {
	Status int             `json:"status"`
	Body   json.RawMessage `json:"body"`
}

func loadFixture(name string) (fixture, error) {
	raw, err := fixtureFS.ReadFile("testdata/fixtures/" + name + ".json")
	if err != nil {
		return fixture{}, fmt.Errorf("fixture %q: %w", name, err)
	}
	var f fixture
	if err := json.Unmarshal(raw, &f); err != nil {
		return fixture{}, fmt.Errorf("fixture %q: %w", name, err)
	}
	return f, nil
}

// FixtureMap decodes a fixture body into a mutable map.
func FixtureMap(name string) (map[string]any, error) {
	return fixtureMap(name)
}

func fixtureMap(name string) (map[string]any, error) {
	f, err := loadFixture(name)
	if err != nil {
		return nil, err
	}
	var m map[string]any
	if err := json.Unmarshal(f.Body, &m); err != nil {
		return nil, fmt.Errorf("fixture %q is not an object: %w", name, err)
	}
	return m, nil
}

// FixtureSlice decodes a fixture body into a mutable slice.
func FixtureSlice(name string) ([]any, error) {
	f, err := loadFixture(name)
	if err != nil {
		return nil, err
	}
	var s []any
	if err := json.Unmarshal(f.Body, &s); err != nil {
		return nil, fmt.Errorf("fixture %q is not a slice: %w", name, err)
	}
	return s, nil
}
