package main

import (
	"os"

	"github.com/tmc/langchaingo/llms/openai"
)

type ChatCompletion struct {
	url   string
	model string
	token string
}
type Option func(*ChatCompletion)

func WithBaseURL(url string) Option {
	return func(cc *ChatCompletion) {
		cc.url = url
	}
}

func WithToken(token string) Option {
	return func(cc *ChatCompletion) {
		cc.token = token
	}
}

func WithModel(model string) Option {
	return func(cc *ChatCompletion) {
		cc.model = model
	}
}

func NewLLM(options ...Option) (*openai.LLM, error) {
	cc := ChatCompletion{
		url:   "https://api.groq.com/openai/v1",
		model: "openai/gpt-oss-120b",
		token: os.Getenv("GROQ_API_KEY"),
	}
	for _, option := range options {
		option(&cc)
	}

	llm, err := openai.New(openai.WithBaseURL(cc.url), openai.WithToken(cc.token), openai.WithModel(cc.model))

	if err != nil {
		return llm, err
	}
	return llm, nil
}
