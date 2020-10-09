# mijiamon

Poll Xiaomi Mijia temperature/humidity sensors, send data to InfluxDB.

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

## Bugs

Not really a bug but `go-ble` doesn't support more than one client at a time so attempting to poll a lot of devices with long timeouts and a short polling interval won't work.
