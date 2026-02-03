package main

import (
	"context"
	"fmt"
	"lelang/piper"
	"log"
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
	sampleRate = 16000
	channels   = 1
	groqAPIURL = "https://api.groq.com/openai/v1/audio/transcriptions"
)

// WAV header constants
const (
	wavHeaderSize = 44
)

type GroqTranscriptionResponse struct {
	Text string `json:"text"`
}

type model struct {
	llmChain    *chains.LLMChain
	viewport    viewport.Model
	content     string
	ready       bool
	recorder    *Recorder
	apiKey      string
	piperVoice  *piper.PiperVoice
	status      string
	focusWord   int
	focusRow    int
	cancelSpeak context.CancelFunc
}

func initialModel(apiKey string) model {
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
	piperVoice := piper.NewPiperVoice()
	return model{
		llmChain:   llmChain,
		recorder:   NewRecorder(),
		apiKey:     apiKey,
		status:     "Ready",
		piperVoice: piperVoice,
	}
}

func (m model) Init() tea.Cmd {
	return nil
}

func EmptyCmd() tea.Msg {
	return ""
}

type RecordingStarted struct{}
type TranscriptionReceived struct {
	transcription string
}
type StatusChanged struct {
	status string
}
type ReadyCompletion struct {
	completion string
	addContent bool
}

func GetLlmCompletion(text string, m model) tea.Cmd {
	return func() tea.Msg {
		output, err := chains.Call(context.Background(), m.llmChain, map[string]any{"text": text})
		if err != nil {
			return StatusChanged{status: "Failed get completion"}
		}
		if output["text"] == nil {
			return StatusChanged{status: "No completion"}
		}
		return ReadyCompletion{completion: output["text"].(string), addContent: true}
	}
}

type DownloadModel struct {
	model      string
	language   string
	completion string
}

func Speak(ctx context.Context, text string, m model) tea.Cmd {
	return func() tea.Msg {
		err := m.piperVoice.Speak(ctx, text)
		if err != nil {
			switch err := err.(type) {
			case piper.StoppedSpeaking:
				return ""
			case piper.ErrorModelNotFound:
				return DownloadModel{model: err.Model, language: err.Language, completion: text}
			default:
				log.Printf("Error speaking: %v\n", err)
				return StatusChanged{status: "Failed to speak"}
			}
		}
		return StatusChanged{status: "Ready"}
	}
}

func HighlightFocusWord(m model) string {
	var st strings.Builder

	wrappedC := lipgloss.NewStyle().Width(m.viewport.Width).Render(strings.TrimSpace(m.content))

	log.Printf("Not wrappe:  %q, Wrapped split %q, %q", strings.Split(strings.TrimSpace(m.content), " "), strings.Split(strings.TrimSpace(m.content), " "), wrappedC)

	for i, row := range strings.Split(strings.TrimSpace(m.content), "\n") {
		if i == m.focusRow {
			for i, word := range strings.Split(strings.TrimSpace(row), " ") {
				if i == m.focusWord {
					log.Printf("FocusWord: %q %v", word, i)
					st.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("205")).Render(word))
				} else {
					st.WriteString(word)
				}
				st.WriteRune(' ')
			}
		} else {
			st.WriteString(row)
		}
		st.WriteRune('\n')
	}

	return st.String()
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case DownloadModel:
		m.status = "Downloading tts model"
		return m, func() tea.Msg {
			err := piper.DownloadVoice(msg.language, msg.model)
			if err != nil {
				return StatusChanged{status: "Failed to download model"}
			}
			return ReadyCompletion{completion: msg.completion, addContent: false}
		}

	case StatusChanged:
		m.status = msg.status
	case ReadyCompletion:
		if msg.addContent {
			wrappedCompletion := lipgloss.NewStyle().Width(m.viewport.Width).Render(msg.completion)
			m.content = fmt.Sprintf("%sAI: %s \n", m.content, wrappedCompletion)
			highlightedCompletion := HighlightFocusWord(m)
			m.viewport.SetContent(highlightedCompletion)
			m.viewport.GotoBottom()
		}

		m.status = "Speaking"
		ctx, cancel := context.WithCancel(context.Background())
		m.cancelSpeak = cancel
		return m, Speak(ctx, msg.completion, m)

	case TranscriptionReceived:
		wrappedTrascription := lipgloss.NewStyle().Width(m.viewport.Width).Render(msg.transcription)
		m.content = fmt.Sprintf("%sYou:%s \n", m.content, wrappedTrascription)

		highlighted := HighlightFocusWord(m)
		m.viewport.SetContent(highlighted)
		m.viewport.GotoBottom()
		return m, GetLlmCompletion(msg.transcription, m)

	case tea.KeyMsg:
		switch k := msg.String(); k {
		case "esc":
			if m.cancelSpeak != nil {
				m.cancelSpeak()
			}
			m.status = "Ready"
		case "j":
			m.focusRow++

			rows := strings.Split(strings.TrimSpace(m.content), "\n")
			if len(rows) == 0 {
				break
			}

			focusedRow := rows[m.focusRow]
			m.focusWord = min(max(len(strings.Split(strings.TrimSpace(focusedRow), " ")) - 1, 0), m.focusWord)

			highlightedCompletion := HighlightFocusWord(m)
			m.viewport.SetContent(highlightedCompletion)
		case "k":
			if m.focusRow - 1 < 0 {
				break
			}
			m.focusRow--
			rows := strings.Split(strings.TrimSpace(m.content), "\n")
			if len(rows) == 0 {
				break
			}

			focusedRow := rows[m.focusRow]
			m.focusWord = min(max(len(strings.Split(strings.TrimSpace(focusedRow), " ")) - 1, 0), m.focusWord)

			highlightedCompletion := HighlightFocusWord(m)
			m.viewport.SetContent(highlightedCompletion)
		case "w":
			rows := strings.Split(strings.TrimSpace(m.content), "\n")
			if len(rows) == 0 {
				break
			}

			focusedRow := rows[m.focusRow]
			if m.focusWord+1 >= len(strings.Split(focusedRow, " ")) && m.focusRow+1 >= len(rows) {
				break
			}

			if m.focusWord+1 >= len(strings.Split(strings.TrimSpace(focusedRow), " ")) {
				m.focusRow++
				m.focusWord = -1
			}

			m.focusWord++
			highlightedCompletion := HighlightFocusWord(m)
			m.viewport.SetContent(highlightedCompletion)
		case "b":
			if m.focusWord-1 < 0 && m.focusRow-1 < 0 {
				break
			} else if m.focusWord-1 < 0 {
				m.focusRow = max(0, m.focusRow-1)
				rows := strings.Split(strings.TrimSpace(m.content), "\n")
				focusedRow := strings.Split(strings.TrimSpace(rows[m.focusRow]), " ")
				m.focusWord = len(focusedRow)
			}
			log.Printf("FocusWord B: %v %v", m.focusWord, m.focusRow)

			m.focusWord--
			highlightedCompletion := HighlightFocusWord(m)
			m.viewport.SetContent(highlightedCompletion)
		case "ctrl+b":
			if m.cancelSpeak != nil {
				m.cancelSpeak()
			}

			if time.Since(m.recorder.Stopped) < time.Second {
				return m, EmptyCmd
			}

			if m.recorder.IsRecording() {
				m.recorder.Stop()
				m.status = "Ready"
				return m, func() tea.Msg {
					transcription, err := transcribeWithGroq(m.recorder.Content, m.apiKey)
					log.Println(transcription)
					if err != nil {
						log.Printf("Error transcribing audio: %v\n", err)
						return EmptyCmd
					}
					return TranscriptionReceived{transcription: transcription}
				}
			}

			m.status = "Recording"
			return m, func() tea.Msg {
				m.recorder.Start()
				return ""
			}
		case "ctrl+c", "q":
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
	b.BottomRight = "┴"
	return lipgloss.NewStyle().BorderStyle(b).Padding(0, 1)
}()

func (m model) headerView() string {
	title := titleStyle.Render("LeLang")

	blockLength := max(0, m.viewport.Width-lipgloss.Width(title))

	line := strings.Repeat("─", blockLength)

	statusLength := max(0, blockLength-lipgloss.Width(m.status))
	statusLine := strings.Repeat(" ", statusLength) + m.status

	s := lipgloss.JoinVertical(lipgloss.Center, statusLine, line)

	return lipgloss.JoinHorizontal(lipgloss.Center, title, s)
}

func (m model) View() string {
	return fmt.Sprintf("%s\n%s\n", m.headerView(), m.viewport.View())
}

func main() {
	apiKey := os.Getenv("GROQ_API_KEY")
	if apiKey == "" {
		fmt.Println("Error: GROQ_API_KEY environment variable not set")
		os.Exit(1)
	}

	p := tea.NewProgram(
		initialModel(apiKey),
		tea.WithAltScreen(),       // use the full size of the terminal in its "alternate screen buffer"
		tea.WithMouseCellMotion(), // turn on mouse support so we can track the mouse wheel
	)
	f, err := tea.LogToFile("tea.log", "")
	if err != nil {
		fmt.Println("could not run program:", err)
		os.Exit(1)
	}
	defer f.Close()

	if _, err := p.Run(); err != nil {
		fmt.Println("could not run program:", err)
		os.Exit(1)
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
