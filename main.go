package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"lelang/piper"
	"mime/multipart"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/gordonklaus/portaudio"
	"github.com/tmc/langchaingo/chains"
	"github.com/tmc/langchaingo/memory"
	"github.com/tmc/langchaingo/prompts"
	"golang.org/x/term"
)

const (
	sampleRate      = 16000
	channels        = 1
	framesPerBuffer = 1024
	groqAPIURL      = "https://api.groq.com/openai/v1/audio/transcriptions"
)

// WAV header constants
const (
	wavHeaderSize = 44
)

type GroqTranscriptionResponse struct {
	Text string `json:"text"`
}

func main() {
	apiKey := os.Getenv("GROQ_API_KEY")
	if apiKey == "" {
		fmt.Println("Error: GROQ_API_KEY environment variable not set")
		os.Exit(1)
	}

	fmt.Println("Voice Assistant")
	fmt.Println("===============")
	fmt.Println("Press Ctrl+B to start recording, Ctrl+C to quit")

	llm, err := NewLLM()
	if err != nil {
		fmt.Printf("Error creating LLM: %v\n", err)
		os.Exit(1)
	}

	prompt := prompts.NewPromptTemplate(
		`Du bist ein deutscher Lehrer. Antworte auf die folgende Frage oder Aussage auf Deutsch.

Bisheriger Gesprächsverlauf:
{{.history}}

Schüler: {{.text}}
Lehrer:`,
		[]string{"history", "text"},
	)
	llmChain := chains.NewLLMChain(llm, prompt)
	llmChain.Memory = memory.NewConversationBuffer()

	piperVoice := piper.NewPiperVoice()

	// Main loop - wait for Ctrl+B
	for {
		fmt.Println("\n[Waiting] Press Ctrl+B to start recording...")

		if err := waitForCtrlB(); err != nil {
			fmt.Printf("Error: %v\n", err)
			continue
		}

		// Record audio
		fmt.Println("\n[1/4] Recording audio... (Press Ctrl+C to stop)")
		audioData, err := recordAudio()
		if err != nil {
			fmt.Printf("Error recording audio: %v\n", err)
			continue
		}
		fmt.Printf("Recorded %d bytes of audio\n", len(audioData))

		// Transcribe with Groq
		fmt.Println("\n[2/4] Transcribing audio with Groq...")
		transcription, err := transcribeWithGroq(audioData, apiKey)
		if err != nil {
			fmt.Printf("Error transcribing audio: %v\n", err)
			continue
		}
		fmt.Printf("Transcription: %s\n", transcription)

		// Generate LLM response
		fmt.Println("\n[3/4] Generating response...")
		output, err := chains.Call(context.Background(), llmChain, map[string]any{"text": transcription})
		if err != nil {
			fmt.Printf("Error generating chat completion: %v\n", err)
			continue
		}
		completion := output["text"].(string)
		fmt.Printf("Response: %s\n", completion)

		// Generate speech with Piper TTS
		fmt.Println("\n[4/4] Generating speech with Piper TTS...")
		err = piperVoice.Speak(completion)
		if err != nil {
			fmt.Printf("Error generating speech: %v\n", err)
			continue
		}
		fmt.Println("\nDone!")
	}
}

// waitForCtrlB waits for the user to press Ctrl+B
func waitForCtrlB() error {
	// Save terminal state
	oldState, err := term.MakeRaw(int(os.Stdin.Fd()))
	if err != nil {
		return fmt.Errorf("failed to set raw mode: %w", err)
	}
	defer term.Restore(int(os.Stdin.Fd()), oldState)

	buf := make([]byte, 1)
	for {
		_, err := os.Stdin.Read(buf)
		if err != nil {
			return fmt.Errorf("failed to read input: %w", err)
		}

		// Ctrl+B is ASCII 2
		if buf[0] == 2 {
			return nil
		}
		// Ctrl+C is ASCII 3 - exit program
		if buf[0] == 3 {
			fmt.Println("\nExiting...")
			os.Exit(0)
		}
	}
}

// recordAudio captures audio from the microphone until Ctrl+C is pressed
func recordAudio() ([]byte, error) {
	err := portaudio.Initialize()
	if err != nil {
		return nil, fmt.Errorf("failed to initialize PortAudio: %w", err)
	}
	defer portaudio.Terminate()

	// Create buffer for audio samples
	buffer := make([]int16, framesPerBuffer)
	var allSamples []int16

	// Open default input stream
	stream, err := portaudio.OpenDefaultStream(channels, 0, float64(sampleRate), framesPerBuffer, buffer)
	if err != nil {
		return nil, fmt.Errorf("failed to open stream: %w", err)
	}
	defer stream.Close()

	err = stream.Start()
	if err != nil {
		return nil, fmt.Errorf("failed to start stream: %w", err)
	}

	// Handle Ctrl+C to stop recording
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	done := make(chan bool)

	go func() {
		<-sigChan
		fmt.Println("\nStopping recording...")
		done <- true
	}()

	// Record until signal received
recording:
	for {
		select {
		case <-done:
			break recording
		default:
			err := stream.Read()
			if err != nil {
				return nil, fmt.Errorf("failed to read from stream: %w", err)
			}
			// Copy buffer to avoid overwriting
			samples := make([]int16, len(buffer))
			copy(samples, buffer)
			allSamples = append(allSamples, samples...)
		}
	}

	err = stream.Stop()
	if err != nil {
		return nil, fmt.Errorf("failed to stop stream: %w", err)
	}

	// Convert to WAV format
	wavData := samplesToWAV(allSamples, sampleRate, channels)
	return wavData, nil
}

// samplesToWAV converts raw audio samples to WAV format
func samplesToWAV(samples []int16, sampleRate, channels int) []byte {
	var buf bytes.Buffer

	dataSize := len(samples) * 2 // 2 bytes per sample (16-bit)
	fileSize := wavHeaderSize + dataSize - 8

	// RIFF header
	buf.WriteString("RIFF")
	binary.Write(&buf, binary.LittleEndian, int32(fileSize))
	buf.WriteString("WAVE")

	// fmt subchunk
	buf.WriteString("fmt ")
	binary.Write(&buf, binary.LittleEndian, int32(16))          // Subchunk1Size (16 for PCM)
	binary.Write(&buf, binary.LittleEndian, int16(1))           // AudioFormat (1 for PCM)
	binary.Write(&buf, binary.LittleEndian, int16(channels))    // NumChannels
	binary.Write(&buf, binary.LittleEndian, int32(sampleRate))  // SampleRate
	byteRate := sampleRate * channels * 2                       // ByteRate
	binary.Write(&buf, binary.LittleEndian, int32(byteRate))
	blockAlign := channels * 2                                  // BlockAlign
	binary.Write(&buf, binary.LittleEndian, int16(blockAlign))
	binary.Write(&buf, binary.LittleEndian, int16(16))          // BitsPerSample

	// data subchunk
	buf.WriteString("data")
	binary.Write(&buf, binary.LittleEndian, int32(dataSize))

	// Write audio data
	for _, sample := range samples {
		binary.Write(&buf, binary.LittleEndian, sample)
	}

	return buf.Bytes()
}

// transcribeWithGroq sends audio to Groq API for transcription
func transcribeWithGroq(audioData []byte, apiKey string) (string, error) {
	var requestBody bytes.Buffer
	writer := multipart.NewWriter(&requestBody)

	// Add audio file
	part, err := writer.CreateFormFile("file", "audio.wav")
	if err != nil {
		return "", fmt.Errorf("failed to create form file: %w", err)
	}
	_, err = part.Write(audioData)
	if err != nil {
		return "", fmt.Errorf("failed to write audio data: %w", err)
	}

	// Add model field
	err = writer.WriteField("model", "whisper-large-v3")
	if err != nil {
		return "", fmt.Errorf("failed to write model field: %w", err)
	}

	// Add response format
	err = writer.WriteField("response_format", "json")
	if err != nil {
		return "", fmt.Errorf("failed to write response_format field: %w", err)
	}

	err = writer.Close()
	if err != nil {
		return "", fmt.Errorf("failed to close writer: %w", err)
	}

	// Create request
	req, err := http.NewRequest("POST", groqAPIURL, &requestBody)
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", writer.FormDataContentType())

	// Send request
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("API error (status %d): %s", resp.StatusCode, string(body))
	}

	var transcriptionResp GroqTranscriptionResponse
	err = json.Unmarshal(body, &transcriptionResp)
	if err != nil {
		return "", fmt.Errorf("failed to parse response: %w", err)
	}

	return transcriptionResp.Text, nil
}

