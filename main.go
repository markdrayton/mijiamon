package main

import (
	"context"
	"encoding/binary"
	"encoding/hex"
	"flag"
	"fmt"
	"log"
	"net/http"
	_ "net/http/pprof"
	"strings"
	"sync"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/go-ble/ble"
	"github.com/go-ble/ble/linux"
	influxdb2 "github.com/influxdata/influxdb-client-go/v2"
)

type Data map[string]interface{}

type Config struct {
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
		Type string
	}
}

type sensor struct {
	name      string
	data      Data
	mu        *sync.Mutex
	processor func([]byte) Data
}

func newSensor(name string, processor func([]byte) Data) *sensor {
	return &sensor{
		name:      name,
		data:      make(Data),
		mu:        &sync.Mutex{},
		processor: processor,
	}
}

func (s *sensor) processAdv(b []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for k, v := range s.processor(b) {
		s.data[k] = v
	}
}

func (s *sensor) flush() Data {
	s.mu.Lock()
	defer s.mu.Unlock()
	ret := make(Data)
	for k, v := range s.data {
		ret[k] = v
	}
	s.data = make(Data)
	return ret
}

func processAdvLYWSD03MMC(b []byte) Data {
	// assumes https://github.com/pvvx/ATC_MiThermometer firmware
	if len(b) == 15 {
		return Data{
			"temperature": float64(int16(binary.LittleEndian.Uint16(b[6:8]))) / 100,
			"humidity":    float64(binary.LittleEndian.Uint16(b[8:10])) / 100,
			"battery_pct": int(b[12]),
		}
	}
	return Data{}
}

func processAdvLYWSDCGQ(b []byte) Data {
	switch int(b[13]) {
	case 0x01:
		return Data{
			"battery_pct": int(b[14]),
		}
	case 0x04:
		return Data{
			"temperature": float64(int16(binary.LittleEndian.Uint16(b[14:16]))) / 10,
			"humidity":    float64(binary.LittleEndian.Uint16(b[16:18])) / 10,
		}
	}
	return Data{}
}

var (
	configFile string
	dryRun     bool
	verbose    bool
	sensors    map[string]*sensor
)

func init() {
	log.SetFlags(log.Ldate | log.Lmicroseconds)

	d, err := linux.NewDevice()
	if err != nil {
		log.Fatal("Can't create new device:", err)
	}
	ble.SetDefaultDevice(d)

	flag.StringVar(&configFile, "c", "config.toml", "config file path")
	flag.BoolVar(&dryRun, "n", false, "dry run mode")
	flag.BoolVar(&verbose, "v", false, "verbose logginge")
	flag.Parse()

	sensors = make(map[string]*sensor)
}

func vlog(fmt string, a ...interface{}) {
	if verbose {
		log.Printf(fmt, a...)
	}
}

func formatHex(b []byte) string {
	h := hex.EncodeToString(b)
	out := ""
	i := 0
	for i < len(h) {
		out += h[i : i+2]
		i += 2
		if i != len(h) {
			out += " "
		}
	}
	return out
}

func advHandler(a ble.Advertisement) {
	s := sensors[a.Addr().String()]
	for _, sd := range a.ServiceData() {
		vlog("adv: %s, UUID: %s, data (len %d): %s",
			s.name, sd.UUID.String(), len(sd.Data), formatHex(sd.Data))
		s.processAdv(sd.Data)
	}
}

func advFilter(a ble.Advertisement) bool {
	_, ok := sensors[a.Addr().String()]
	return ok
}

func main() {
	go func() {
		log.Println(http.ListenAndServe(":6060", nil))
	}()

	var conf Config
	_, err := toml.DecodeFile(configFile, &conf)
	if err != nil {
		log.Fatal(err)
	}

	url := fmt.Sprintf("http://%s:%d/", conf.Database.Host, conf.Database.Port)
	client := influxdb2.NewClient(url, conf.Database.User+":"+conf.Database.Pass)
	writeAPI := client.WriteAPIBlocking("", conf.Database.Name)

	for _, s := range conf.Sensors {
		mac := strings.ToLower(s.Mac)
		switch s.Type {
		case "LYWSD03MMC":
			sensors[mac] = newSensor(s.Name, processAdvLYWSD03MMC)
		case "LYWSDCGQ/01ZM":
			sensors[mac] = newSensor(s.Name, processAdvLYWSDCGQ)
		default:
			log.Fatalf("unknown sensor type %s", s.Type)
		}
	}

	go func() {
		ticker := time.NewTicker(1 * time.Minute)
		for {
			<-ticker.C
			for _, s := range sensors {
				fields := s.flush()
				log.Printf("%s %+v\n", s.name, fields)
				if !dryRun && len(fields) > 0 {
					p := influxdb2.NewPoint(
						"environment",
						map[string]string{
							"name": s.name,
						},
						fields,
						time.Now(),
					)
					err := writeAPI.WritePoint(context.Background(), p)
					if err != nil {
						fmt.Printf("Write error: %s\n", err.Error())
					}
				}
			}
		}
	}()

	log.Print("starting scan")

	ctx := ble.WithSigHandler(context.WithCancel(context.Background()))
	ble.Scan(ctx, true, advHandler, advFilter)
}
