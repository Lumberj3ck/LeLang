# LazyLang

A terminal-based language learning app that lets you have conversations with an AI while translating unfamiliar words on the fly — all without leaving the terminal.


<img width="1902" height="1062" alt="screenshot-2026-02-07_16-56-41" src="https://github.com/user-attachments/assets/5430b492-eca4-4049-ab88-21b1d3592462" />



## Why

When practicing a foreign language through conversation, you constantly run into words you don't know. The usual workflow is: stop what you're doing, open a dictionary in a browser, look up the word, switch back. This context switching breaks your focus and slows you down.

LazyLang solves this by putting the conversation and the dictionary in the same place. You speak, the AI responds in language you speak, and you navigate the response with vim keybindings to translate any word instantly. No tab switching, no copy-pasting — just stay in the flow.

### Controls

| Key | Action |
|---|---|
| `Ctrl+B` | Start/stop recording |
| `j` / `k` | Move focus down/up one line |
| `w` / `b` | Move focus to next/previous word |
| `Enter` | Translate focused word |
| `Esc` | Stop speech playback |
| `q` / `Ctrl+C` | Quit |

### Requirements

- [Groq API key](https://console.groq.com) (for speech recognition and LLM)
- [LibreTranslate](https://github.com/LibreTranslate/LibreTranslate) instance for word translation
- [Piper TTS](https://github.com/rhasspy/piper) for text-to-speech (included in Docker image)

### Running with Docker

Create a `.env` file with your `GROQ_API_KEY`, then:

```bash
docker compose up --build
```

This starts both the app and a LibreTranslate instance.

To run the app container directly (with an external LibreTranslate):

```bash
docker build -t lazylang .
docker run --env-file .env --name lazylang --rm -it \
  -e PULSE_SERVER=/run/user/1000/pulse/native \
  -v /run/user/1000/pulse:/run/user/1000/pulse \
  -v ~/.config/pulse/cookie:/root/.config/pulse/cookie \
  --group-add $(getent group audio | cut -d: -f3) \
  lazylang
```

## Contributing

1. Fork the repository
2. Create a feature branch
3. Make your changes
4. Open a pull request
