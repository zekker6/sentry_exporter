package main

import (
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"regexp"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/prometheus/common/version"
	"gopkg.in/yaml.v3"
)

type Config struct {
	Modules map[string]Module `yaml:"modules"`
}

type SafeConfig struct {
	sync.RWMutex
	C *Config
}

type Module struct {
	Prober  string        `yaml:"prober"`
	Timeout time.Duration `yaml:"timeout"`
	HTTP    HTTPProbe     `yaml:"http"`
}

type HTTPProbe struct {
	// Defaults to 2xx.
	ValidStatusCodes []int             `yaml:"valid_status_codes"`
	Prefix           string            `yaml:"prefix"`
	Headers          map[string]string `yaml:"headers"`
}

var Probers = map[string]func(string, http.ResponseWriter, Module) bool{
	"http": probeHTTP,
}

func (sc *SafeConfig) reloadConfig(confFile string) (err error) {
	var c = &Config{}

	yamlFile, err := os.ReadFile(confFile)
	if err != nil {
		log.Println("Error reading config file: ", err)
		return err
	}

	if err := yaml.Unmarshal(yamlFile, c); err != nil {
		log.Println("Error parsing config file: ", err)
		return err
	}

	sc.Lock()
	sc.C = c
	sc.Unlock()

	log.Println("Loaded config file")
	return nil
}

func probeHandler(w http.ResponseWriter, r *http.Request, conf *Config) {
	params := r.URL.Query()
	target := params.Get("target")
	if target == "" {
		http.Error(w, "Target parameter is missing", 400)
		return
	}

	moduleName := params.Get("module")
	if moduleName == "" {
		moduleName = "sentry"
	}
	module, ok := conf.Modules[moduleName]
	if !ok {
		http.Error(w, fmt.Sprintf("Unknown module %q", moduleName), 400)
		return
	}
	prober, ok := Probers[module.Prober]
	if !ok {
		http.Error(w, fmt.Sprintf("Unknown prober %q", module.Prober), 400)
		return
	}

	start := time.Now()
	success := prober(target, w, module)
	if _, err := fmt.Fprintf(w, "sentry_probe_duration_seconds %f\n", time.Since(start).Seconds()); err != nil {
		log.Println("error writing probe duration metric:", err)
	}
	if success {
		if _, err := fmt.Fprintln(w, "sentry_probe_success 1"); err != nil {
			log.Println("error writing probe success metric:", err)
		}
	} else {
		if _, err := fmt.Fprintln(w, "sentry_probe_success 0"); err != nil {
			log.Println("error writing probe success metric:", err)
		}
	}
}

func probeHTTP(target string, w http.ResponseWriter, module Module) (success bool) {
	config := module.HTTP

	client := &http.Client{
		Timeout: module.Timeout,
	}

	requestURL := config.Prefix + target + "/stats/"

	request, err := http.NewRequest("GET", requestURL, nil)
	if err != nil {
		log.Println("Error creating request for target", target, err)
		return
	}

	for key, value := range config.Headers {
		if http.CanonicalHeaderKey(key) == "Host" {
			request.Host = value
			continue
		}
		request.Header.Set(key, value)
	}

	resp, err := client.Do(request)
	// Err won't be nil if redirects were turned off. See https://github.com/golang/go/issues/3795
	if err != nil && resp == nil {
		log.Println("Error for HTTP request to", target, err)
	} else {
		defer func() {
			if err := resp.Body.Close(); err != nil {
				log.Println("error closing response body:", err)
			}
		}()
		if len(config.ValidStatusCodes) != 0 {
			for _, code := range config.ValidStatusCodes {
				if resp.StatusCode == code {
					success = true
					break
				}
			}
		} else if 200 <= resp.StatusCode && resp.StatusCode < 300 {
			success = true
		}
		if success {
			if _, err := fmt.Fprintf(w, "sentry_probe_error_received %d\n", extractErrorRate(resp.Body, config)); err != nil {
				log.Println("error writing error received metric:", err)
			}
		}
	}
	if resp == nil {
		resp = &http.Response{}
	}

	if _, err := fmt.Fprintf(w, "sentry_probe_status_code %d\n", resp.StatusCode); err != nil {
		log.Println("error writing status code metric:", err)
	}
	if _, err := fmt.Fprintf(w, "sentry_probe_content_length %d\n", resp.ContentLength); err != nil {
		log.Println("error writing content length metric:", err)
	}

	return
}

func extractErrorRate(reader io.Reader, config HTTPProbe) int {
	var re = regexp.MustCompile(`(\d+)]]$`)
	body, err := io.ReadAll(reader)
	if err != nil {
		log.Println("Error reading HTTP body", err)
		return 0
	}
	var str = string(body)
	matches := re.FindStringSubmatch(str)
	value, err := strconv.Atoi(matches[1])
	if err == nil {
		return value
	}
	return 0
}

func main() {
	var (
		configFile    = flag.String("config.file", "sentry_exporter.yml", "Sentry exporter configuration file.")
		listenAddress = flag.String("web.listen-address", ":9412", "The address to listen on for HTTP requests.")
		showVersion   = flag.Bool("version", false, "Print version information.")
		sc            = &SafeConfig{
			C: &Config{},
		}
	)
	flag.Parse()

	if *showVersion {
		if _, err := fmt.Fprintln(os.Stdout, version.Print("sentry_exporter")); err != nil {
			log.Println("error writing version:", err)
		}
		os.Exit(0)
	}

	log.Println("Starting sentry_exporter", version.Info())
	log.Println("Build context", version.BuildContext())

	if err := sc.reloadConfig(*configFile); err != nil {
		log.Fatalf("Error loading config: %s", err)
	}

	hup := make(chan os.Signal, 1)
	reloadCh := make(chan chan error)
	signal.Notify(hup, syscall.SIGHUP)
	go func() {
		for {
			select {
			case <-hup:
				if err := sc.reloadConfig(*configFile); err != nil {
					log.Println("Error reloading config: ", err)
				}
			case rc := <-reloadCh:
				if err := sc.reloadConfig(*configFile); err != nil {
					log.Println("Error reloading config: ", err)
					rc <- err
				} else {
					rc <- nil
				}
			}
		}
	}()

	http.Handle("/metrics", promhttp.Handler())
	http.HandleFunc("/probe",
		func(w http.ResponseWriter, r *http.Request) {
			sc.RLock()
			c := sc.C
			sc.RUnlock()

			probeHandler(w, r, c)
		})
	http.HandleFunc("/-/reload",
		func(w http.ResponseWriter, r *http.Request) {
			if r.Method != "POST" {
				w.WriteHeader(http.StatusMethodNotAllowed)
				if _, err := fmt.Fprintf(w, "This endpoint requires a POST request.\n"); err != nil {
					log.Println("error writing method not allowed response:", err)
				}
				return
			}

			rc := make(chan error)
			reloadCh <- rc
			if err := <-rc; err != nil {
				http.Error(w, fmt.Sprintf("failed to reload config: %s", err), http.StatusInternalServerError)
			}
		})
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if _, err := w.Write([]byte(`<html>
            <head><title>Sentry Exporter</title></head>
            <body>
            <h1>Sentry Exporter</h1>
            <p><a href="/probe?target=apimutate">Probe sentry project</a></p>
            <p><a href="/metrics">Metrics</a></p>
            </body>
            </html>`)); err != nil {
			log.Println("error writing root HTML:", err)
		}
	})

	log.Println("Listening on", *listenAddress)
	if err := http.ListenAndServe(*listenAddress, nil); err != nil {
		log.Fatalf("Error starting HTTP server: %s", err)
	}
}
