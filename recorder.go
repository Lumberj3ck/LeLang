package main

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"time"

	"github.com/gordonklaus/portaudio"
)


type Recorder struct {
	recording bool
	content   []byte
	done      chan struct{}
	Stopped  time.Time
}

func NewRecorder() *Recorder {
	return &Recorder{
		recording: false,
		done:      make(chan struct{}),
	}
}

func (r *Recorder) Recording() bool {
	return r.recording
}

// recordAudio captures audio from the microphone until Ctrl+B is pressed
func (r *Recorder) Start() ([]byte, error) {

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

	r.recording = true

	log.Println("Recording")
	// Record until signal received
	recording:
		for {
			select {
			case <-r.done:
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
	r.content = wavData
	return wavData, nil
}

func (r *Recorder) Stop() {
	if !r.recording {
		return
	}
	r.done <- struct{}{}
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
	binary.Write(&buf, binary.LittleEndian, int32(16))         // Subchunk1Size (16 for PCM)
	binary.Write(&buf, binary.LittleEndian, int16(1))          // AudioFormat (1 for PCM)
	binary.Write(&buf, binary.LittleEndian, int16(channels))   // NumChannels
	binary.Write(&buf, binary.LittleEndian, int32(sampleRate)) // SampleRate
	byteRate := sampleRate * channels * 2                      // ByteRate
	binary.Write(&buf, binary.LittleEndian, int32(byteRate))
	blockAlign := channels * 2 // BlockAlign
	binary.Write(&buf, binary.LittleEndian, int16(blockAlign))
	binary.Write(&buf, binary.LittleEndian, int16(16)) // BitsPerSample

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
