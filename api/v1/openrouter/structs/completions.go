package structs

import "encoding/json"

type CompletionsRequest struct {
	Model            string             `json:"model" validate:"required"`
	Prompt           json.RawMessage    `json:"prompt" validate:"required"`
	BestOf           int                `json:"best_of"` // defaults to 1
	Echo             bool               `json:"echo"`
	FrequencyPenalty float64            `json:"frequency_penalty"`
	LogitBias        map[string]float64 `json:"logit_bias"`
	LogProbs         int                `json:"logprobs"`
	MaxTokens        int                `json:"max_tokens"`
	N                int                `json:"n"`
	PresencePenalty  float64            `json:"presence_penalty"`
	Seed             int                `json:"seed"`
	Stop             json.RawMessage    `json:"stop"`
	Stream           bool               `json:"stream"`
	StreamOptions    CReqStreamOptions  `json:"stream_options"`
	Suffix           string             `json:"suffix"`
	Temperature      float64            `json:"temperature"`
	TopP             float64            `json:"top_p"`
	User             string             `json:"user"`
}

func (cr *CompletionsRequest) SetDefaultValues() {
	if cr.BestOf == 0 {
		cr.BestOf = 1
	}
}

type CReqStreamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

type CompletionsResponse struct {
	Id                string       `json:"id"`
	Object            string       `json:"object"`
	Created           int64        `json:"created"`
	Model             string       `json:"model"`
	SystemFingerprint string       `json:"system_fingerprint"`
	Choices           []CResChoice `json:"choices"`
	Usage             CReqUsage    `json:"usage"`
}

type CResChoice struct {
	Text         string `json:"text"`
	Index        int    `json:"index"`
	LogProbs     string `json:"logprobs"`
	FinishReason string `json:"finish_reason"`
}

type CReqUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}
