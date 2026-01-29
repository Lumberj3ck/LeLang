package main

import (
	"fmt"
	"os"
	"path/filepath"
)

func saveToFile(data []byte, filename string) error {
	home, err := os.UserHomeDir()

	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}

	voicesDir := filepath.Join(home, ".piper-voices")
	err = os.MkdirAll(voicesDir, 0755)
	fmt.Println(err)

	_, err = os.OpenFile(filepath.Join(voicesDir, "test.wav"), os.O_RDWR|os.O_CREATE, 0666)
	fmt.Println(err)

	return nil
}
