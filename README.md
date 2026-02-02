Piper tts is a requirement for this project.


Build image:
```bash
docker build -t lelang .
```
Run with docker:

Provide GROQ_API_KEY in .env file

```bash
docker run  --env-file .env --name lelang --rm -it   -e PULSE_SERVER=/run/user/1000/pulse/native  -v /run/user/1000/pulse:/run/user/1000/pulse \
  -v ~/.config/pulse/cookie:/root/.config/pulse/cookie \
  --group-add $(getent group audio | cut -d: -f3) \
  lelang
```

