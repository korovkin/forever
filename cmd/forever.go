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
)

type logger struct {
	commandNum    int
	iteration     int
	isError       bool
	buf           *bytes.Buffer
	print         bool
	commandNumber int
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

func (l *logger) Write(p []byte) (int, error) {
	buf := bytes.NewBuffer(p)
	wrote := 0
	for {
		line, err := buf.ReadBytes('\n')
		if len(line) > 1 {
			now := time.Now().Format("15:01:02")
			s := string(line)
			ts := time.Since(loggerStartTime).String()
			e := "I"
			if l.isError {
				e = "E"
			}

			{
				loggerMutex.Lock()
				if l.print {
					ct.ChangeColor(loggerColors[l.commandNumber%len(loggerColors)], false, ct.None, false)
					fmt.Printf("[l:%03d-%04d: %-14s %s %s] ", l.commandNumber, l.iteration, ts, now, e)
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

func newLogger(commandNum int, collectLines bool) *logger {
	l := &logger{commandNum: commandNum, iteration: 0, buf: nil}
	if collectLines {
		l.buf = &bytes.Buffer{}
	}
	l.print = true
	return l
}

func executeCommand(p *Forever, iteration int, commandLine string, commandNumber int) error {
	p.StatNumCommandsStart.Inc()
	T_START := time.Now()
	var err error
	loggerOut := newLogger(commandNumber, true)
	loggerOut.isError = false
	loggerOut.iteration = iteration
	loggerErr := newLogger(commandNumber, true)
	loggerErr.isError = true
	loggerErr.iteration = iteration

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
			p.StatNumCommandsDone.Inc()
		} else {
			p.StatNumCommandsError.Inc()
		}
		p.StatCommandLatency.Observe(dt.Seconds())
	}()

	// execute locally:
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
		"=> start",
		"iter:", iteration,
		"cmdNum:", commandNumber,
		"cmd: ", commandLine))

	loggerOut.print = *flag_verbose
	loggerOut.commandNumber = commandNumber
	loggerErr.print = *flag_verbose
	loggerErr.commandNumber = commandNumber

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

type Forever struct {
	jobs   int
	logger *logger
	worker *limiter.ConcurrencyLimiter

	// stats:
	StatNumCommandsStart prometheus.Counter
	StatNumCommandsDone  prometheus.Counter
	StatNumCommandsError prometheus.Counter
	StatCommandLatency   prometheus.Summary
}

func (p *Forever) Close() {
	p.worker.Wait()
}

func (p *Forever) Run() {
	var err error

	log.SetFlags(log.Lmicroseconds | log.Ldate | log.Lshortfile)
	gotils.CheckFatal(err)

	r := bufio.NewReaderSize(os.Stdin, 1*1024*1024)
	fmt.Fprintf(p.logger, "reading from stdin...\n")
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
			io.WriteString(c,
				fmt.Sprintf(
					"go  time: %d slave_address: %s jobs: %d",
					time.Now().Unix(),
					serverAddress,
					p.jobs))
		})

	err := http.ListenAndServe(serverAddress, nil)
	if err != nil {
		log.Println("WARNING: failed to start the metrics server on:", serverAddress, err.Error)
	}
}

func setupPromMetrics(p *Forever, metricsAddress string) {
	p.StatNumCommandsStart = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "commands_num_start",
			Help: "num started"})
	err := prometheus.Register(p.StatNumCommandsStart)
	gotils.CheckFatal(err)

	p.StatNumCommandsDone = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "commands_num_done",
			Help: "num completed - ok"})
	err = prometheus.Register(p.StatNumCommandsDone)
	gotils.CheckFatal(err)

	p.StatNumCommandsError = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "commands_num_error",
			Help: "num completed - error"})
	err = prometheus.Register(p.StatNumCommandsError)
	gotils.CheckFatal(err)

	p.StatCommandLatency = prometheus.NewSummary(prometheus.SummaryOpts{
		Name: "commands_latency",
		Help: "commands latency stat",
	})
	err = prometheus.Register(p.StatCommandLatency)
	gotils.CheckFatal(err)

	// run the metrics server:
	go metricsServer(p, metricsAddress)
}

func main() {
	T_START := time.Now()
	logger := newLogger(0, false)
	defer func() {
		fmt.Fprintf(logger, "all done: dt: "+time.Since(T_START).String()+"\n")
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
	fmt.Fprintf(logger, "concurrency limit: %d", *flag_jobs)

	p := &Forever{}
	p.jobs = *flag_jobs
	p.logger = logger
	p.worker = limiter.NewConcurrencyLimiter(p.jobs)

	defer p.Close()

	setupPromMetrics(p, *flag_metrics_address)

	fmt.Fprintf(logger, fmt.Sprintln("running as master"))
	p.Run()
}
