# mijiamon

Listen for temperature/humidity advertisements from Xiaomi Mijia sensors (specifically, models LYWSD03MMC and LYWSDCGQ/01ZM), send data to InfluxDB.

## Building/installing

```sh
$ cp config.toml.example config.toml
$ $EDITOR config.toml
$ go build
$ sudo ./mijiamon
```

Or with Docker:

```sh
$ cp config.toml.example config.toml
$ $EDITOR config.toml
$ docker build -t mijiamon .
$ docker run --network host --privileged --rm --name mijiamon mijiamon
```

(`--privileged` to access `hci0` etc; could probably be improved)
