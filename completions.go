package main

import (
	"context"
	"os"

	"github.com/tmc/langchaingo/llms"
	"github.com/tmc/langchaingo/llms/openai"
)

type ChatCompletion struct{
	url string
	model string
	token string
}
type Option func(*ChatCompletion)

func WithBaseURL(url string) Option{
	return func(cc *ChatCompletion) {
		cc.url = url
	}
}

func WithToken(token string) Option{
	return func(cc *ChatCompletion) {
		cc.token = token
	}
}

func WithModel(model string) Option{
	return func(cc *ChatCompletion) {
		cc.model = model
	}
}

func generateChatCompletion(prompt string, options ...Option) (string, error) {
	token := os.Getenv("GROQ_API_KEY")

	cc := ChatCompletion{
		url: "https://api.groq.com/openai/v1",
		model: "llama-3.1-8b-instant",
		token: token,
	}

	for _, option := range options {
		option(&cc)
	}

	llm, err := openai.New(openai.WithBaseURL(cc.url), openai.WithToken(cc.token), openai.WithModel(cc.model))
	if err != nil {
		return "", err
	}

	completion, err := llms.GenerateFromSinglePrompt(context.Background(), llm, prompt)
	if err != nil {
		return "", err
	}

	return completion, nil
}
