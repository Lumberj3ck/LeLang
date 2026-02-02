FROM golang:1.24-bookworm

WORKDIR /app

RUN apt-get update && apt-get install -y \
    alsa-utils \
    pulseaudio-utils \
    && rm -rf /var/lib/apt/lists/*

RUN wget https://github.com/rhasspy/piper/releases/download/v1.2.0/piper_amd64.tar.gz
RUN tar -xzf piper_amd64.tar.gz
RUN mv piper/* /usr/local/bin/
RUN mv /usr/local/bin/piper  /usr/local/bin/piper-tts

COPY . .
RUN go mod download

RUN go build -o lelang .

CMD ["./lelang"]
