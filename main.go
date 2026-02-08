package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"lazylang/piper"
	"log"
	"log/slog"
	"net/http"
	"os"
	"regexp"
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
	groqAPIBaseURL = "https://api.groq.com/openai/v1"
)
var groqAudioAPIURL = fmt.Sprintf("%v/audio/transcriptions", groqAPIBaseURL)


// WAV header constants
const (
	wavHeaderSize = 44
)

const (
	scrolloff = 2
)

var isAlpha = regexp.MustCompile(`[\p{L}]+`)

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
	fullWidth   int
	cancelSpeak context.CancelFunc
	wordsStore  *WordsStore
	config      Config
}

func initialModel(apiKey string, config Config) model {
	llm, err := NewLLM()
	if err != nil {
		fmt.Printf("Error creating LLM: %v\n", err)
		os.Exit(1)
	}

	prompt := prompts.NewPromptTemplate(
		fmt.Sprintf(` You are a %s teacher. Respond to the following question or statement in
  %s.

  Previous conversation history:
  {{.history}}

  Important: only give short answers to the questions!
  Student: {{.text}}
  Teacher:
  `, config.Language, config.Language),
		[]string{"history", "text"},
	)

	llmChain := chains.NewLLMChain(llm, prompt)
	llmChain.Memory = memory.NewConversationBuffer()
	piperVoice := piper.NewPiperVoice(piper.WithModel(config.TTSBackend.Voice), piper.WithLanguage(config.Language))
	return model{
		llmChain:   llmChain,
		recorder:   NewRecorder(),
		apiKey:     apiKey,
		status:     "Ready",
		piperVoice: piperVoice,
		wordsStore: NewWordsStore(),
		config:     config,
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

func HighlightFocusWord(wrapped_text string, focusRow int, focusWord int) string {
	var st strings.Builder
	for i, row := range strings.Split(strings.TrimSpace(wrapped_text), "\n") {
		if i == focusRow {
			for i, word := range strings.Split(strings.TrimSpace(row), " ") {
				if i == focusWord {
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

type TranslationReceived struct {
	Word        string
	Translation string
}

func GetTranslation(word string, m model) tea.Cmd {
	return func() tea.Msg {
		baseURL := os.Getenv("LIBRETRANSLATE_URL")
		if baseURL == "" {
			baseURL = m.config.LibreTranslateURL
		}

		reqBody, err := json.Marshal(map[string]string{
			"q":      word,
			"source": m.config.Language,
			"target": m.config.TargetTranslationLanguage,
			"format": "text",
		})
		if err != nil {
			log.Printf("Error marshaling translation request: %v", err)
			return StatusChanged{status: "Failed to translate"}
		}

		resp, err := http.Post(baseURL+"/translate", "application/json", bytes.NewReader(reqBody))
		if err != nil {
			log.Printf("Error calling LibreTranslate: %v", err)
			return StatusChanged{status: "Failed to translate"}
		}
		defer resp.Body.Close()

		body, err := io.ReadAll(resp.Body)
		if err != nil {
			log.Printf("Error reading translation response: %v", err)
			return StatusChanged{status: "Failed to translate"}
		}

		if resp.StatusCode != http.StatusOK {
			log.Printf("LibreTranslate error (status %d): %s", resp.StatusCode, string(body))
			return StatusChanged{status: "Failed to translate"}
		}

		var result struct {
			TranslatedText string `json:"translatedText"`
		}
		if err := json.Unmarshal(body, &result); err != nil {
			log.Printf("Error parsing translation response: %v", err)
			return StatusChanged{status: "Failed to translate"}
		}

		return TranslationReceived{Word: word, Translation: result.TranslatedText}
	}
}

func (m *model) UpdateStatus(status string) {
	if m.recorder.IsRecording() || m.piperVoice.IsSpeaking() {
		return
	}
	m.status = status
}

func setViewportContent(m *model, content string) {
	content = lipgloss.NewStyle().Width(m.viewport.Width).Render(content)
	m.viewport.SetContent(content)
}

func getWrappedContent(content string, width int) string {
	return lipgloss.NewStyle().Width(width).Render(content)
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case DownloadModel:
		m.UpdateStatus("Downloading tts model")
		return m, func() tea.Msg {
			err := piper.DownloadVoice(msg.language, msg.model)
			if err != nil {
				return StatusChanged{status: "Failed to download model"}
			}
			return ReadyCompletion{completion: msg.completion, addContent: false}
		}

	case StatusChanged:
		m.UpdateStatus(msg.status)
	case ReadyCompletion:
		if msg.addContent {
			sanitisedCompletion := strings.ReplaceAll(msg.completion, "\n\n", "\n")
			m.content = fmt.Sprintf("%sAI: %s \n", m.content, sanitisedCompletion)
			highlightedCompletion := HighlightFocusWord(m.content, m.focusRow, m.focusWord)
			setViewportContent(&m, highlightedCompletion)
			m.viewport.GotoBottom()
		}

		m.UpdateStatus("Speaking")

		ctx, cancel := context.WithCancel(context.Background())
		m.cancelSpeak = cancel
		return m, Speak(ctx, msg.completion, m)

	case TranscriptionReceived:
		m.content = fmt.Sprintf("%sYou:%s \n", m.content, msg.transcription)
		highlighted := HighlightFocusWord(m.content, m.focusRow, m.focusWord)
		setViewportContent(&m, highlighted)
		m.viewport.GotoBottom()
		return m, GetLlmCompletion(msg.transcription, m)

	case TranslationReceived:
		m.wordsStore.Add(msg.Word, msg.Translation)

	case tea.KeyMsg:
		switch k := msg.String(); k {
		case "enter":
			selectedWord := m.getFocusedWord()
			clearedWord := isAlpha.FindString(selectedWord)
			if clearedWord == "" {
				m.UpdateStatus("Nothing to translate")
				return m, EmptyCmd
			}
			return m, GetTranslation(clearedWord, m)

		case "esc":
			if m.cancelSpeak != nil {
				m.cancelSpeak()
			}
			m.UpdateStatus("Ready")
		case "j":
			wrappedCompletion := getWrappedContent(m.content, m.viewport.Width)
			rows := strings.Split(strings.TrimSpace(wrappedCompletion), "\n")
			if len(rows) == 0 || len(wrappedCompletion) == 0 {
				break
			}
			if m.focusRow+1 >= len(rows) {
				break
			}
			m.focusRow++

			focusedRow := rows[m.focusRow]
			m.focusWord = min(max(len(strings.Split(strings.TrimSpace(focusedRow), " "))-1, 0), m.focusWord)

			highlightedCompletion := HighlightFocusWord(wrappedCompletion, m.focusRow, m.focusWord)
			setViewportContent(&m, highlightedCompletion)
			log.Printf("FocusWord j: %v %v", m.focusWord, m.focusRow)

			// If we're not at scrolloff, don't scroll
			visibleLines := m.viewport.VisibleLineCount()
			if (visibleLines+m.viewport.YOffset)-m.focusRow > scrolloff {
				return m, EmptyCmd
			}
		case "k":
			if m.focusRow-1 < 0 {
				break
			}
			m.focusRow--

			wrappedCompletion := getWrappedContent(m.content, m.viewport.Width)
			rows := strings.Split(strings.TrimSpace(wrappedCompletion), "\n")
			if len(rows) == 0 {
				break
			}

			focusedRow := rows[m.focusRow]
			m.focusWord = min(max(len(strings.Split(strings.TrimSpace(focusedRow), " "))-1, 0), m.focusWord)

			highlightedCompletion := HighlightFocusWord(wrappedCompletion, m.focusRow, m.focusWord)
			setViewportContent(&m, highlightedCompletion)

			// If we're not at scrolloff, don't scroll
			if m.focusRow-(m.viewport.YOffset-1) > scrolloff {
				return m, EmptyCmd
			}
		case "w":
			wrappedCompletion := getWrappedContent(m.content, m.viewport.Width)
			rows := strings.Split(strings.TrimSpace(wrappedCompletion), "\n")
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
			highlightedCompletion := HighlightFocusWord(wrappedCompletion, m.focusRow, m.focusWord)
			setViewportContent(&m, highlightedCompletion)

			// If we're not at scrolloff, don't scroll
			visibleLines := m.viewport.VisibleLineCount()
			if (visibleLines+m.viewport.YOffset)-m.focusRow > scrolloff {
				return m, EmptyCmd
			}
			m.viewport.ScrollDown(1)
		case "b":
			wrappedCompletion := getWrappedContent(m.content, m.viewport.Width)
			if m.focusWord-1 < 0 && m.focusRow-1 < 0 {
				break
			} else if m.focusWord-1 < 0 {
				m.focusRow = max(0, m.focusRow-1)
				rows := strings.Split(strings.TrimSpace(wrappedCompletion), "\n")
				focusedRow := strings.Split(strings.TrimSpace(rows[m.focusRow]), " ")
				m.focusWord = len(focusedRow)
			}

			m.focusWord--
			highlightedCompletion := HighlightFocusWord(wrappedCompletion, m.focusRow, m.focusWord)
			setViewportContent(&m, highlightedCompletion)

			// If we're not at scrolloff, don't scroll
			if m.focusRow-(m.viewport.YOffset-1) > scrolloff {
				return m, EmptyCmd
			}
			m.viewport.ScrollUp(1)
			return m, EmptyCmd
		case "ctrl+b":
			if m.cancelSpeak != nil {
				m.cancelSpeak()
			}

			if time.Since(m.recorder.Stopped) < time.Second {
				return m, EmptyCmd
			}

			if m.recorder.IsRecording() {
				m.recorder.Stop()
				m.UpdateStatus("Ready")
				return m, func() tea.Msg {
					transcription, err := transcribeWithGroq(m.recorder.Content, m.apiKey, m.config.Language)
					log.Println(transcription)
					if err != nil {
						log.Printf("Error transcribing audio: %v\n", err)
						return EmptyCmd
					}
					return TranscriptionReceived{transcription: transcription}
				}
			}

			m.UpdateStatus("Recording")
			return m, func() tea.Msg {
				m.recorder.Start()
				return ""
			}
		case "ctrl+c", "q":
			return m, tea.Quit
		}
	case tea.WindowSizeMsg:
		m.fullWidth = msg.Width
		headerHeight := lipgloss.Height(m.headerView()) + 1
		viewportWidth := msg.Width*3/4 + 1
		viewportHeight := msg.Height - headerHeight

		if !m.ready {
			viewport := viewport.New(viewportWidth, viewportHeight)
			viewport.YPosition = headerHeight
			viewport.SetContent(m.content)
			m.viewport = viewport
			m.ready = true
		} else {
			m.viewport.Width = viewportWidth
			m.viewport.Height = viewportHeight
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

func (m model) getFocusedWord() string {
	rows := strings.Split(strings.TrimSpace(m.content), "\n")

	row := strings.TrimSpace(rows[m.focusRow])
	return strings.Split(row, " ")[m.focusWord]
}

func (m model) headerView() string {
	title := titleStyle.Render("LazyLang")

	blockLength := max(0, m.fullWidth-lipgloss.Width(title))

	line := strings.Repeat("─", blockLength)

	statusLength := max(0, blockLength-lipgloss.Width(m.status))
	statusLine := strings.Repeat(" ", statusLength) + m.status

	s := lipgloss.JoinVertical(lipgloss.Center, statusLine, line)

	return lipgloss.JoinHorizontal(lipgloss.Center, title, s)
}

func (m model) sidebarView() string {
	b := lipgloss.NewStyle().
		Height(m.viewport.Height).
		Width(m.fullWidth*1/4 - 1).
		Border(lipgloss.NormalBorder()).
		BorderLeft(true).
		BorderTop(false).
		BorderRight(false).
		BorderBottom(false)

	return b.Render(m.wordsStore.List())
}

func (m model) View() string {
	content := lipgloss.JoinHorizontal(lipgloss.Center, m.viewport.View(), m.sidebarView())
	return fmt.Sprintf("%s\n%s\n", m.headerView(), content)
}

func main() {
	apiKey := os.Getenv("GROQ_API_KEY")
	if apiKey == "" {
		fmt.Println("Error: GROQ_API_KEY environment variable not set")
		os.Exit(1)
	}

	config, err := GetConfig(apiKey)

    var syntaxErr *json.SyntaxError
	if errors.As(err, &syntaxErr) {
		log.Fatalf("Error parsing config: %v", syntaxErr)
	} else if errors.Is(err, invalidApiKey) {
		log.Fatalf("Error: Invalid API key")
	}

	if err != nil {
		slog.Error("Failed to get config", "error", err)
	}
	slog.Info("Config", "config", config)


	p := tea.NewProgram(
		initialModel(apiKey, config),
		tea.WithAltScreen(),       // use the full size of the terminal in its "alternate screen buffer"
		tea.WithMouseCellMotion(), // turn on mouse support so we can track the mouse wheel
	)
	f, err := tea.LogToFile("tea.log", "")
	if err != nil {
		fmt.Println("could not run program:", err)
		os.Exit(1)
	}
	defer f.Close()

	m, err := p.Run()
	my := m.(model)
	if my.cancelSpeak != nil {
		my.cancelSpeak()
	}

	if err != nil {
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
