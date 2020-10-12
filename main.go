package main

import (
	"context"
	"fmt"
	"log"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/go-ble/ble"
	"github.com/go-ble/ble/linux"
	"github.com/influxdata/influxdb-client-go/v2"
)

var (
	temperatureAndHumidityUUID = ble.MustParse("226caa5564764566756266734470666d")
	batteryPctUUID             = ble.UUID16(0x2a19)
	mutex                      = &sync.Mutex{} // ble library isn't thread-safe
)

type Sensor struct {
	Mac      string
	Name     string
	Timeout  time.Duration
	Interval time.Duration
}

type Result struct {
	Name         string
	Time         time.Time
	Temperature  float64
	Humidity     float64
	BatteryPct   int
	PollDuration time.Duration
}

type Config struct {
	Timeout  int
	Interval int
	Database struct {
		Host string
		Port int
		User string
		Pass string
		Name string
	}
	Sensors []struct {
		Mac  string
		Name string
	}
}

func NewSensor(mac, name string, timeout, interval time.Duration) *Sensor {
	return &Sensor{
		Mac:      mac,
		Name:     name,
		Timeout:  timeout,
		Interval: interval,
	}
}

func (s *Sensor) readBatteryLevel(c ble.Client, p *ble.Profile) (int, error) {
	chr := p.Find(ble.NewCharacteristic(batteryPctUUID))
	b, err := c.ReadCharacteristic(chr.(*ble.Characteristic))
	if err != nil {
		return 0, err
	}
	return int(b[0]), nil
}

func (s *Sensor) readTemperatureAndHumidity(ctx context.Context, c ble.Client, p *ble.Profile) ([2]float64, error) {
	ch := make(chan []byte)

	chr := p.Find(ble.NewCharacteristic(temperatureAndHumidityUUID))
	c.Subscribe(chr.(*ble.Characteristic), false, func(req []byte) {
		ch <- req
	})
	defer c.Unsubscribe(chr.(*ble.Characteristic), false)

	res := [2]float64{}

	select {
	case req := <-ch:
		// T=23.7 H=55.2
		data := strings.Trim(string(req), "\x00")
		pairs := strings.Split(data, " ")
		for i, pair := range pairs {
			parts := strings.Split(pair, "=")
			v, err := strconv.ParseFloat(parts[1], 64)
			if err != nil {
				return res, err
			}
			res[i] = v
		}
	case <-ctx.Done():
		return res, ctx.Err()
	}

	return res, nil
}

func (s *Sensor) poll(results chan Result) {
	mutex.Lock()
	defer mutex.Unlock()

	start := time.Now()
	done := make(chan struct{})
	ctx, cancel := context.WithTimeout(context.Background(), s.Timeout)
	defer cancel()

	addr := ble.NewAddr(s.Mac)
	client, err := ble.Dial(ctx, addr)
	log.Printf("%s: connecting\n", s.Name)
	if err != nil {
		log.Printf("Error dialing %s: %s", s.Name, err)
		return
	}

	go func() {
		<-client.Disconnected()
		log.Printf("%s: disconnected\n", s.Name)
		close(done)
	}()

	profile, err := client.DiscoverProfile(true)
	if err != nil {
		log.Printf("%s: %s\n", s.Name, err)
		return
	}

	pct, err := s.readBatteryLevel(client, profile)
	if err != nil {
		log.Printf("%s: %s\n", s.Name, err)
		return
	}

	th, err := s.readTemperatureAndHumidity(ctx, client, profile)
	if err != nil {
		log.Printf("%s: %s\n", s.Name, err)
		return
	}

	results <- Result{
		Name:         s.Name,
		Time:         start,
		Temperature:  th[0],
		Humidity:     th[1],
		BatteryPct:   pct,
		PollDuration: time.Now().Sub(start),
	}

	client.CancelConnection()
	<-done
}

func (s *Sensor) Run(results chan Result) {
	ticker := time.NewTicker(s.Interval)
	for ; true; <-ticker.C {
		s.poll(results)
	}
}

func init() {
	d, err := linux.NewDevice()
	if err != nil {
		log.Fatal("Can't create new device:", err)
	}
	ble.SetDefaultDevice(d)
}

func main() {
	log.SetFlags(log.Ldate | log.Lmicroseconds)

	var conf Config
	_, err := toml.DecodeFile("config.toml", &conf)
	if err != nil {
		log.Fatal(err)
	}

	url := fmt.Sprintf("http://%s:%d/", conf.Database.Host, conf.Database.Port)
	client := influxdb2.NewClient(url, conf.Database.User+":"+conf.Database.Pass)
	writeAPI := client.WriteAPIBlocking("", conf.Database.Name)

	results := make(chan Result)

	var sensors []*Sensor
	for _, s := range conf.Sensors {
		sensor := NewSensor(
			s.Mac,
			s.Name,
			time.Duration(conf.Timeout)*time.Second,
			time.Duration(conf.Interval)*time.Second,
		)
		sensors = append(sensors, sensor)
		go sensor.Run(results)
	}

	for {
		r := <-results
		log.Printf("%+v\n", r)
		p := influxdb2.NewPoint(
			"environment",
			map[string]string{
				"name": r.Name,
			},
			map[string]interface{}{
				"temperature":      r.Temperature,
				"humidity":         r.Humidity,
				"battery_pct":      r.BatteryPct,
				"poll_duration_ms": r.PollDuration.Milliseconds(),
			},
			r.Time,
		)
		err := writeAPI.WritePoint(context.Background(), p)
		if err != nil {
			fmt.Printf("Write error: %s\n", err.Error())
		}
	}
}
