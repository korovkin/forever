package main

import (
	"bufio"
	"bytes"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	// "context"
	// "runtime/debug"

	ct "github.com/daviddengcn/go-colortext"

	"github.com/korovkin/gotils"
	"github.com/korovkin/limiter"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	dto "github.com/prometheus/client_model/go"
)

type ForeverLogger struct {
	PrevPrint     time.Time            `json:"prev_print"`
	CommandNum    int                  `json:"cmd_num"`
	CommandConfig ForeverCommandConfig `json:"cmd_config"`
	Iteration     int                  `json:"cmd_iteration"`
	IsError       bool                 `json:"err"`
	IsPrint       bool                 `json:"is_print"`

	buf *bytes.Buffer
}

var (
	loggerMutex     = new(sync.Mutex)
	loggerIndex     = int(0)
	loggerStartTime = time.Now()
	loggerHostname  = ""

	flag_verbose = flag.Bool(
		"v",
		true,
		"verbose level: 1")
)

var loggerColors = []ct.Color{
	ct.Green,
	ct.Cyan,
	ct.Magenta,
	ct.Yellow,
	ct.Blue,
	ct.Red,
}

func (l *ForeverLogger) Write(p []byte) (int, error) {
	buf := bytes.NewBuffer(p)
	wrote := 0
	for {
		line, err := buf.ReadBytes('\n')
		if len(line) > 1 {
			s := string(line)
			e := "I"
			if l.IsError {
				e = "E"
			}
			{
				loggerMutex.Lock()
				dt := time.Since(l.PrevPrint)
				now := time.Now().Format("15:01:02")
				name := l.CommandConfig.Name
				if strings.TrimSpace(name) == "" {
					name = fmt.Sprintf("%10d", l.CommandNum)
				} else {
					name = fmt.Sprintf("%-10s", name)
				}

				if l.IsPrint {
					ct.ChangeColor(
						loggerColors[l.CommandNum%len(loggerColors)], false, ct.None, false)
					fmt.Printf("[%-10s i:%04d-%04d: %s %5dms %s] ",
						name,
						l.CommandNum,
						l.Iteration,
						now,
						dt.Milliseconds(),
						e)

					ct.ResetColor()
					fmt.Print(s)
				}
				if l.buf != nil {
					l.buf.Write([]byte(s))
				}

				loggerMutex.Unlock()
			}

			wrote += len(line)
		}
		if err != nil {
			break
		}
	}
	if len(p) > 0 && p[len(p)-1] != '\n' {
		fmt.Println()
	}

	return len(p), nil
}

func newLogger(commandNum int, collectLines bool) *ForeverLogger {
	l := &ForeverLogger{CommandNum: commandNum, Iteration: 0}
	l.PrevPrint = time.Now()
	if collectLines {
		l.buf = &bytes.Buffer{}
	}
	l.IsPrint = true
	return l
}

func executeCommand(p *Forever, iteration int, commandLine string, commandNumber int) error {

	T_START := time.Now()
	var err error
	loggerOut := newLogger(commandNumber, true)
	loggerOut.IsError = false
	loggerOut.Iteration = iteration
	loggerErr := newLogger(commandNumber, true)
	loggerErr.IsError = true
	loggerErr.Iteration = iteration

	commandConfig := &ForeverCommandConfig{}
	{
		commandLineComp := strings.Split(commandLine, "#FOREVER:")
		if len(commandLineComp) > 1 {
			gotils.FromJSONString(commandLineComp[1], commandConfig)
		}
		if strings.TrimSpace(commandConfig.Name) == "" {
			commandConfig.Name = fmt.Sprintf("%d", commandNumber)
		} else {
			commandConfig.Name = fmt.Sprintf("%s", commandConfig.Name)
		}
		loggerOut.CommandConfig = *commandConfig
		loggerErr.CommandConfig = *commandConfig
	}

	labels := map[string]string{
		"name": "cmd_name",
		"arg":  commandConfig.Name,
	}

	p.StatNumCommandsStart.With(labels).Inc()
	p.StatNumCommandsStart.WithLabelValues("cmd_name", "all").Inc()

	defer func() {
		dt := time.Since(T_START)
		fmt.Fprintf(
			loggerOut,
			fmt.Sprintln(
				"=> done:",
				"iter:", iteration,
				"cmdNum:", commandNumber,
				"cmd:", commandLine,
				"dt:", dt.String()))

		if err == nil {
			p.StatNumCommandsDone.With(labels).Inc()
		} else {
			p.StatNumCommandsError.With(labels).Inc()
		}
		p.StatCommandLatency.With(labels).Observe(dt.Seconds())
	}()

	cs := []string{"/bin/bash", "-c", commandLine}
	cmd := exec.Command(cs[0], cs[1:]...)
	cmd.Stdin = nil
	cmd.Stdout = loggerOut
	cmd.Stderr = loggerErr
	cmd.Env = append(
		os.Environ(),
		fmt.Sprintf("FOREVER_ITERATION=%d", iteration),
	)

	fmt.Fprintf(loggerOut, fmt.Sprintln(
		"iter:", iteration,
		"cmdNum:", commandNumber,
		"cmd: ", commandLine))

	loggerOut.IsPrint = *flag_verbose
	loggerOut.CommandNum = commandNumber
	loggerErr.IsPrint = *flag_verbose
	loggerErr.CommandNum = commandNumber

	err = cmd.Start()
	gotils.CheckFatal(err)
	if err != nil {
		log.Fatalln("failed to start:", err)
		return err
	}

	if err == nil {
		err = cmd.Wait()
	}

	return err
}

type ForeverCommandConfig struct {
	Name string `json:"name"`
}

type Forever struct {
	ConcurrentCommands int                         `json:"concurrent_cmds"`
	worker             *limiter.ConcurrencyLimiter `json:"-"`

	// stats:
	StatNumCommandsStart *prometheus.CounterVec `json:"-"`
	StatNumCommandsDone  *prometheus.CounterVec `json:"-"`
	StatNumCommandsError *prometheus.CounterVec `json:"-"`
	StatCommandLatency   *prometheus.SummaryVec `json:"-"`
}

func (p *Forever) Close() {
	p.worker.Wait()
}

func (p *Forever) Run() {
	var err error

	log.SetFlags(log.Lmicroseconds | log.Ldate | log.Lshortfile)
	gotils.CheckFatal(err)

	r := bufio.NewReaderSize(os.Stdin, 1*1024*1024)
	log.Println("reading from stdin...\n")
	commandNum := 0
	for {
		line, err := r.ReadString('\n')
		if err == io.EOF {
			break
		}
		line = strings.TrimSpace(line)

		commandNumber := commandNum
		p.worker.ExecuteWithTicket(func(ticket int) {
			for iteration := 0; true; iteration += 1 {
				executeCommand(p, iteration, line, commandNumber)
			}
		})

		commandNum += 1
	}
}

func metricsServer(p *Forever, serverAddress string) {
	metricsHandler := promhttp.HandlerFor(prometheus.DefaultGatherer, promhttp.HandlerOpts{})
	http.HandleFunc("/metrics", func(c http.ResponseWriter, req *http.Request) {
		metricsHandler.ServeHTTP(c, req)
	})

	http.HandleFunc("/",
		func(c http.ResponseWriter, req *http.Request) {
			now := time.Now()

			starts := float64(0)
			var m = &dto.Metric{}
			if err := p.StatNumCommandsStart.WithLabelValues("cmd_name", "all").Write(m); err != nil {
			} else {
				starts = m.Counter.GetValue()
			}

			io.WriteString(c,
				gotils.ToJSONString(map[string]interface{}{
					"now":      now,
					"now_unix": now.Unix(),
					"address":  serverAddress,
					"forever":  p,
					"starts":   starts,
				}))
		})

	err := http.ListenAndServe(serverAddress, nil)
	if err != nil {
		log.Println("WARNING: failed to start the metrics server on:", serverAddress, err.Error())
	}
}

func setupPromMetrics(p *Forever, metricsAddress string) {
	labels := []string{"name", "arg"}

	p.StatNumCommandsStart = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "commands_num_start",
			Help: "num started"},
		labels)
	err := prometheus.Register(p.StatNumCommandsStart)
	gotils.CheckFatal(err)

	p.StatNumCommandsDone = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "commands_num_done",
			Help: "num completed - ok"}, labels)
	err = prometheus.Register(p.StatNumCommandsDone)
	gotils.CheckFatal(err)

	p.StatNumCommandsError = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "commands_num_error",
			Help: "num completed - error"}, labels)
	err = prometheus.Register(p.StatNumCommandsError)
	gotils.CheckFatal(err)

	p.StatCommandLatency = prometheus.NewSummaryVec(
		prometheus.SummaryOpts{
			Name: "commands_latency",
			Help: "commands latency stat",
		}, labels)
	err = prometheus.Register(p.StatCommandLatency)
	gotils.CheckFatal(err)

	// run the metrics server:
	go metricsServer(p, metricsAddress)
}

func main() {
	T_START := time.Now()
	defer func() {
		log.Println("all done: dt: " + time.Since(T_START).String() + "\n")
	}()

	flag_jobs := flag.Int(
		"j",
		100,
		"num of concurrent jobs")

	flag_metrics_address := flag.String(
		"metrics_address",
		"localhost:9105",
		"prometheus metrics address")

	loggerHostname, _ = os.Hostname()

	flag.Parse()
	log.Println("concurrency limit: %d", *flag_jobs)

	p := &Forever{}
	p.ConcurrentCommands = *flag_jobs
	p.worker = limiter.NewConcurrencyLimiter(p.ConcurrentCommands)
	defer p.Close()

	setupPromMetrics(p, *flag_metrics_address)
	p.Run()
}
