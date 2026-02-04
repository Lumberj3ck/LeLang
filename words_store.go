package main

import (
	"fmt"
	"strings"
)


type WordsStore struct {
	words map[string]string
	order []string
}

func NewWordsStore() *WordsStore {
	return &WordsStore{
		words: make(map[string]string),
		order: []string{},
	}
}

func (ws *WordsStore) List() string {
	var s strings.Builder
	for _, word := range ws.order {
		fmt.Fprintf(&s, "%s: %s\n", word, ws.words[word])
	}
	return s.String()
}

func (ws *WordsStore) Add(word string, meaning string) {
	if  _, ok := ws.words[word]; !ok{
		ws.order = append(ws.order, word)
	}
	ws.words[word] = meaning
}

