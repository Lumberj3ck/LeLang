package main

import (
	"context"
	"flag"
	"fmt"
	"lelang/piper"
	"math/rand"
	"os"
	"strings"
	"time"

	"github.com/tmc/langchaingo/chains"
	"github.com/tmc/langchaingo/memory"
	"github.com/tmc/langchaingo/prompts"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
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

type model struct {
	llmChain *chains.LLMChain
	viewport viewport.Model
	content  string
	ready    bool
	recorder *Recorder
}

func initialModel() model {
	llm, err := NewLLM()
	if err != nil {
		fmt.Printf("Error creating LLM: %v\n", err)
		os.Exit(1)
	}

	prompt := prompts.NewPromptTemplate(
		`Du bist ein deutscher Lehrer. Antworte auf die folgende Frage oder Aussage auf Deutsch.

Bisheriger Gesprächsverlauf:
{{.history}}

Wichtig geben Sie nur kurze Antworten auf die Fragen!
Schüler: {{.text}}
Lehrer:`,
		[]string{"history", "text"},
	)

	llmChain := chains.NewLLMChain(llm, prompt)
	llmChain.Memory = memory.NewConversationBuffer()

	var content strings.Builder
	for range 1000 {
		s := "alsd;fasfjqpowejr1384j195j0234jl"
		ri := rand.Intn(len(s))
		row := strings.Repeat(string(s[ri]), 100)

		content.WriteString(row + "\n")
	}


	return model{
		llmChain: llmChain,
		content: content.String(),
		recorder: NewRecorder(),
	}
}

func (m model) Init() tea.Cmd {
	return nil
}

func EmptyCmd() tea.Msg{
	return ""
}

type RecordingStarted struct{}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch k := msg.String(); k {
		case "ctrl+b":
			if time.Since(m.recorder.Stopped) < time.Second {
				return m, EmptyCmd
			}
			
			if m.recorder.Recording() {
				m.recorder.Stop()
				m.viewport.SetContent("Stopped recording")
				return m, func() tea.Msg {
					return ""
				}
			}

			m.viewport.SetContent("Starting recording...")
			return m, func() tea.Msg {
				m.recorder.Start()
				return ""
			}
		case "ctrl+c", "q", "esc":
			return m, tea.Quit
		}
	case tea.WindowSizeMsg:
		headerHeight := lipgloss.Height(m.headerView()) + 1

		if !m.ready {
			viewport := viewport.New(msg.Width, msg.Height-headerHeight)
			viewport.YPosition = headerHeight
			viewport.SetContent(m.content)
			m.viewport = viewport
			m.ready = true
		} else {
			m.viewport.Width = msg.Width
			m.viewport.Height = msg.Height - headerHeight 
		}
	}

	var cmds []tea.Cmd
	var cmd tea.Cmd

	m.viewport, cmd = m.viewport.Update(msg)
	cmds = append(cmds, cmd)
	return m, tea.Batch(cmds...)
}

var titleStyle = func() lipgloss.Style {
		b := lipgloss.RoundedBorder()
		b.Right = "├"
		return lipgloss.NewStyle().BorderStyle(b).Padding(0, 1)
	}()

func (m model) headerView() string {
	title := titleStyle.Render("Mr. Pager")
	line := strings.Repeat("─", max(0, m.viewport.Width-lipgloss.Width(title)))
	return lipgloss.JoinHorizontal(lipgloss.Center, title, line)
}

func (m model) View() string {
	return fmt.Sprintf("%s\n%s\n", m.headerView(), m.viewport.View())
}

func main() {
	serverTui := flag.Bool("serve-tui", false, "Start a TUI")
	flag.Parse()

	if 	*serverTui {
		p := tea.NewProgram(
			initialModel(),
			tea.WithAltScreen(),       // use the full size of the terminal in its "alternate screen buffer"
			tea.WithMouseCellMotion(), // turn on mouse support so we can track the mouse wheel
		)
		f, err := tea.LogToFile("tea.log", "")
		if err != nil{
			fmt.Println("could not run program:", err)
			os.Exit(1)
		}
		defer f.Close()


		if _, err := p.Run(); err != nil {
			fmt.Println("could not run program:", err)
			os.Exit(1)
		}
	} else {
		loop()
	}
}

func loop() {
	apiKey := os.Getenv("GROQ_API_KEY")
	if apiKey == "" {
		fmt.Println("Error: GROQ_API_KEY environment variable not set")
		os.Exit(1)
	}

	fmt.Println("Voice Assistant")
	fmt.Println("===============")
	fmt.Println("Press Ctrl+B to start recording, Ctrl+B to stop")

	llm, err := NewLLM()
	if err != nil {
		fmt.Printf("Error creating LLM: %v\n", err)
		os.Exit(1)
	}

	prompt := prompts.NewPromptTemplate(
		`Du bist ein deutscher Lehrer. Antworte auf die folgende Frage oder Aussage auf Deutsch.

Bisheriger Gesprächsverlauf:
{{.history}}

Wichtig geben Sie nur kurze Antworten auf die Fragen!
Schüler: {{.text}}
Lehrer:`,
		[]string{"history", "text"},
	)
	llmChain := chains.NewLLMChain(llm, prompt)
	llmChain.Memory = memory.NewConversationBuffer()
	recorder := NewRecorder()

	piperVoice := piper.NewPiperVoice()

	// Main loop - wait for Ctrl+B
	for {
		fmt.Println("\n[Waiting] Press Ctrl+B to start recording...")

		if err := waitForCtrlB(); err != nil {
			fmt.Printf("Error: %v\n", err)
			continue
		}

		// Record audio
		fmt.Println("\n[1/4] Recording audio... (Press Ctrl+B to stop)")
		go recorder.Start()

		fmt.Printf("Recorded %d bytes of audio\n", len(recorder.content))

		recorder.Stop()

		// Transcribe with Groq
		fmt.Println("\n[2/4] Transcribing audio with Groq...")
		transcription, err := transcribeWithGroq(recorder.content, apiKey)
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

