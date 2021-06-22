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
	"runtime/debug"
	"strings"
	"sync"
	"time"

	// "context"

	ct "github.com/daviddengcn/go-colortext"

	"github.com/korovkin/gotils"
	"github.com/korovkin/limiter"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

type logger struct {
	ticket     int
	isError    bool
	buf        *bytes.Buffer
	print      bool
	lineNumber int
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
					ct.ChangeColor(loggerColors[l.ticket%len(loggerColors)], false, ct.None, false)
					fmt.Printf("[l:%03d: %-14s %s %03d %s] ", l.lineNumber, ts, now, l.ticket, e)
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

func newLogger(ticket int, collectLines bool) *logger {
	l := &logger{ticket: ticket, buf: nil}
	if collectLines {
		l.buf = &bytes.Buffer{}
	}
	l.print = true
	return l
}

func CheckFatal(e error) error {
	if e != nil {
		debug.PrintStack()
		log.Println("CHECK: ERROR:", e)
		panic(e)
	}
	return e
}

func CheckNotFatal(e error) error {
	if e != nil {
		debug.PrintStack()
		log.Println("CHECK: ERROR:", e, e.Error())
	}
	return e
}

func executeCommand(p *Forever, ticket int, cmdLine string, lineNumber int) error {
	p.StatNumCommandsStart.Inc()
	T_START := time.Now()
	var err error
	loggerOut := newLogger(ticket, true)
	loggerOut.isError = false
	loggerOut.lineNumber = lineNumber
	loggerErr := newLogger(ticket, true)
	loggerErr.isError = true
	loggerErr.lineNumber = lineNumber

	defer func() {
		dt := time.Since(T_START)
		fmt.Fprintf(
			loggerOut,
			"execute: done: dt: "+dt.String()+"\n",
		)
		if err == nil {
			p.StatNumCommandsDone.Inc()
			p.StatCommandLatency.Observe(dt.Seconds())
		}
	}()

	// execute locally:
	cs := []string{"/bin/bash", "-c", cmdLine}
	cmd := exec.Command(cs[0], cs[1:]...)
	cmd.Stdin = nil
	cmd.Stdout = loggerOut
	cmd.Stderr = loggerErr
	cmd.Env = append(
		os.Environ(),
		fmt.Sprintf("PARALLEL_TICKER=%d", ticket),
	)

	fmt.Fprintf(loggerOut, fmt.Sprintln(
		"=> start",
		"lineNumber:", lineNumber,
		"cmd: ", cmdLine))

	loggerOut.print = *flag_verbose
	loggerOut.lineNumber = lineNumber
	loggerErr.print = *flag_verbose
	loggerErr.lineNumber = lineNumber

	err = cmd.Start()
	gotils.CheckFatal(err)
	if err != nil {
		log.Fatalln("failed to start:", err)
		return err
	}

	if err == nil {
		err = cmd.Wait()
	}

	loggerOut.print = true
	loggerErr.print = true

	// output.Tags = map[string]string{"hostname": loggerHostname}
	// if loggerOut.buf != nil {
	// 	output.Stdout = string(loggerOut.buf.Bytes())
	// }
	// if loggerErr.buf != nil {
	// 	output.Stderr = string(loggerErr.buf.Bytes())
	// }

	return err
}

type Slave struct {
	Address string
}

type Forever struct {
	jobs   int
	logger *logger
	worker *limiter.ConcurrencyLimiter

	// stats:
	StatNumCommandsStart prometheus.Counter
	StatNumCommandsDone  prometheus.Counter
	StatCommandLatency   prometheus.Summary
}

func (p *Forever) Close() {
	p.worker.Wait()
}

func mainMaster(p *Forever) {
	var err error

	log.SetFlags(log.Lmicroseconds | log.Ldate | log.Lshortfile)
	CheckFatal(err)

	r := bufio.NewReaderSize(os.Stdin, 1*1024*1024)
	fmt.Fprintf(p.logger, "reading from stdin...\n")
	lineNum := 0
	for {
		line, err := r.ReadString('\n')
		if err == io.EOF {
			break
		}
		line = strings.TrimSpace(line)

		lineNumber := lineNum
		p.worker.ExecuteWithTicket(func(ticket int) {
			executeCommand(p, ticket, line, lineNumber)
		})

		lineNum += 1
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

func main() {
	T_START := time.Now()
	logger := newLogger(0, false)
	defer func() {
		fmt.Fprintf(logger, "all done: dt: "+time.Since(T_START).String()+"\n")
	}()

	flag_jobs := flag.Int(
		"j",
		2,
		"num of concurrent jobs")

	flag_slaves := flag.String(
		"slaves",
		"",
		"CSV list of slave addresses")

	flag_slave_metrics_address := flag.String(
		"metrics_address",
		"localhost:9011",
		"slave metric address")

	loggerHostname, _ = os.Hostname()

	flag.Parse()
	fmt.Fprintf(logger, "concurrency limit: %d", *flag_jobs)
	fmt.Fprintf(logger, "slaves: %s", *flag_slaves)

	p := &Forever{}
	p.jobs = *flag_jobs
	p.logger = logger
	p.worker = limiter.NewConcurrencyLimiter(p.jobs)

	defer p.Close()

	// stats:
	p.StatNumCommandsStart = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "commands_num_start",
			Help: "num received"})
	err := prometheus.Register(p.StatNumCommandsStart)
	CheckFatal(err)

	p.StatNumCommandsDone = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "commands_num_done",
			Help: "num received"})
	err = prometheus.Register(p.StatNumCommandsDone)
	CheckFatal(err)

	p.StatCommandLatency = prometheus.NewSummary(prometheus.SummaryOpts{
		Name: "commands_latency",
		Help: "commands latency stat",
	})
	err = prometheus.Register(p.StatCommandLatency)
	CheckFatal(err)

	// run the metrics server:
	go metricsServer(p, *flag_slave_metrics_address)

	fmt.Fprintf(logger, "running as master\n")
	mainMaster(p)
}
