package interactive

import (
	"encoding/json"
	"sync"
)

// Choice is one option for ask_user.
type Choice struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

// QuestionPayload is stored in tool.Interrupt info.
type QuestionPayload struct {
	Tool       string   `json:"tool"`
	Prompt     string   `json:"prompt"`
	Choices    []Choice `json:"choices,omitempty"`
	Multiple   bool     `json:"multiple"`
	FreeText   bool     `json:"free_text"`
	QuestionID string   `json:"question_id"`
}

// Answer is submitted from the UI.
type Answer struct {
	SelectedIDs []string `json:"selected_ids,omitempty"`
	FreeText    string   `json:"free_text,omitempty"`
}

func (a Answer) String() string {
	b, _ := json.Marshal(a)
	return string(b)
}

var registry sync.Map // questionID -> QuestionPayload

func Register(q QuestionPayload) {
	registry.Store(q.QuestionID, q)
}

func Get(questionID string) (QuestionPayload, bool) {
	v, ok := registry.Load(questionID)
	if !ok {
		return QuestionPayload{}, false
	}
	return v.(QuestionPayload), true
}

func Delete(questionID string) { registry.Delete(questionID) }
