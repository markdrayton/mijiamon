FROM golang:1-buster

RUN apt-get update
RUN apt-get install -y bluetooth bluez usbutils

WORKDIR /app
COPY . .

RUN go get -d -v ./...
RUN go install -v ./...

ENTRYPOINT ["/bin/bash", "entrypoint.sh"]
