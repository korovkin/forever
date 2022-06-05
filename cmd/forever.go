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

	ct "github.com/daviddengcn/go-colortext"

	"github.com/korovkin/forever"
	"github.com/korovkin/gotils"
	"github.com/korovkin/limiter"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	dto "github.com/prometheus/client_model/go"
	glog "github.com/subchen/go-log"
	"github.com/subchen/go-log/writers"
)

type ForeverLogger struct {
	PrevPrint     time.Time            `json:"prev_print"`
	CommandNum    int                  `json:"cmd_num"`
	CommandConfig ForeverCommandConfig `json:"cmd_config"`
	Iteration     int                  `json:"cmd_iteration"`
	IsErrorStream bool                 `json:"err"`
	IsPrint       bool                 `json:"is_print"`

	buf   *bytes.Buffer
	lines *prometheus.CounterVec
}

var (
	loggerMutex = new(sync.Mutex)

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
			if l.IsErrorStream {
				e = "E"
			}

			{
				loggerMutex.Lock()
				dt := time.Since(l.PrevPrint)
				now := time.Now()
				name := l.CommandConfig.Name
				l.PrevPrint = now

				if strings.TrimSpace(name) == "" {
					name = fmt.Sprintf("%10d", l.CommandNum)
				} else {
					name = fmt.Sprintf("%03d %-10s", l.CommandNum, name)
				}

				if l.IsPrint {
					ct.ChangeColor(
						loggerColors[l.CommandNum%len(loggerColors)], false, ct.None, false)
					line := fmt.Sprintf("[%-10s i:%04d-%04d: %s %5dms %s] ",
						name,
						l.CommandNum,
						l.Iteration,
						now.Local().Format("15:04:05"),
						dt.Milliseconds(),
						e)
					fmt.Print(line)
					ct.ResetColor()
					fmt.Print(s)
					glog.Default.Print(strings.TrimSpace(line + s))
				}
				if l.buf != nil {
					l.buf.Write([]byte(s))
				}
				loggerMutex.Unlock()
			}

			wrote += len(line)
			if l.lines != nil {
				l.lines.WithLabelValues(l.CommandConfig.Name, fmt.Sprintf("%v", l.CommandConfig.Index)).Inc()
				l.lines.WithLabelValues("all", "all").Inc()
			}
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

func quote(s string) string {
	return "'" + s + "'"
}

func executeCommand(p *Forever, iteration int, commandLine string, commandNumber int, commandConfig ForeverCommandConfig) error {
	T_START := time.Now()
	var err error
	loggerOut := newLogger(commandNumber, true)
	loggerOut.IsErrorStream = false
	loggerOut.Iteration = iteration
	loggerOut.lines = p.StatLines

	loggerErr := newLogger(commandNumber, true)
	loggerErr.IsErrorStream = true
	loggerErr.Iteration = iteration
	loggerErr.lines = p.StatLines

	loggerOut.CommandConfig = commandConfig
	loggerErr.CommandConfig = commandConfig

	labels := map[string]string{
		"name":  commandConfig.Name,
		"index": fmt.Sprintf("%v", commandConfig.Index),
	}

	p.StatNumCommandsStart.With(labels).Inc()
	p.StatNumCommandsStart.WithLabelValues("all", "all").Inc()

	defer func() {
		dt := time.Since(T_START)
		fmt.Fprintln(loggerOut,
			"=> done:",
			"iter:", iteration,
			"cmdNum:", commandNumber,
			"cmd:", commandLine,
			"config:", gotils.ToJSONStringNoIndent(commandConfig),
			"dt:", dt.String(),
		)
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
	if commandConfig.WorkingDirectory != "" {
		cmd.Dir = commandConfig.WorkingDirectory
	}
	cmd.Env = append(
		os.Environ(),
		fmt.Sprintf("FOREVER_ITERATION=%v", iteration))
	cmd.Env = append(
		os.Environ(),
		fmt.Sprintf("FOREVER_NAME=%v", commandConfig.Name))

	fmt.Fprintln(loggerOut,
		"iter:", iteration,
		"cmdNum:", commandNumber,
		"cmd: ", quote(commandLine))

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

	glog.Println("process exit code:", commandLine, "exit code:", cmd.ProcessState.ExitCode())

	return err
}

type ForeverCommandConfig struct {
	Index            int    `json:"index"`
	Name             string `json:"name"`
	Repeat           bool   `json:"repeat"`
	Restart          bool   `json:"restart"`
	WorkingDirectory string `json:"cd"`
}

type Forever struct {
	ConcurrentCommands int                         `json:"concurrent_cmds"`
	IsRepeatForever    bool                        `json:"is_repeat"`
	worker             *limiter.ConcurrencyLimiter `json:"-"`

	// stats:
	StatNumCommandsStart *prometheus.CounterVec `json:"-"`
	StatNumCommandsDone  *prometheus.CounterVec `json:"-"`
	StatNumCommandsError *prometheus.CounterVec `json:"-"`
	StatCommandLatency   *prometheus.SummaryVec `json:"-"`
	StatLines            *prometheus.CounterVec `json:"-"`
}

func (p *Forever) Close() {
	p.worker.Wait()
}

func (p *Forever) Run() {
	var err error

	log.SetFlags(log.Lmicroseconds | log.Ldate | log.Lshortfile)
	gotils.CheckFatal(err)

	// current config:
	commandConfig := ForeverCommandConfig{}

	r := bufio.NewReaderSize(os.Stdin, 1*1024*1024)
	log.Println("reading from stdin...")
	commandNumber := 0
	for {
		commandConfig.Index = commandNumber

		line, err := r.ReadString('\n')
		if err == io.EOF {
			break
		}

		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		if strings.HasPrefix(line, "#FOREVER:") {
			commandLineComp := strings.Split(line, "#FOREVER:")
			if len(commandLineComp) > 1 {
				gotils.FromJSONString(commandLineComp[1], &commandConfig)
			}
			if strings.TrimSpace(commandConfig.Name) == "" {
				commandConfig.Name = fmt.Sprintf("%d", commandNumber)
			}
			continue
		} else if strings.HasPrefix(line, "#") {
			continue
		}

		p.worker.ExecuteWithTicket(func(ticket int) {
			for iteration := 0; true; iteration += 1 {
				errr := executeCommand(p, iteration, line, commandNumber, commandConfig)
				if errr != nil {
					log.Println("Exit Error:", errr.Error())
				}

				if commandConfig.Repeat || commandConfig.Restart || p.IsRepeatForever {
					// log.Println("repeat enable - restarting command: [", line, "]")
					glog.Println("repeat enable - restarting command:[", line, "]")
					continue
				} else {
					break
				}
			}
		})

		commandNumber += 1
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
			if err := p.StatNumCommandsStart.WithLabelValues("all", "all").Write(m); err != nil {
			} else {
				starts = m.Counter.GetValue()
			}

			io.WriteString(c,
				gotils.ToJSONString(map[string]interface{}{
					"now":            now,
					"now_unix":       now.Unix(),
					"address":        serverAddress,
					"forever":        p,
					"starts":         starts,
					"version_string": forever.VersionString(),
					"version":        forever.VERSION_NUMBER,
				}))
		})

	err := http.ListenAndServe(serverAddress, nil)
	if err != nil {
		log.Println("WARNING: failed to start the metrics server on:", serverAddress, err.Error())
	}
}

func setupPromMetrics(p *Forever, metricsAddress string) {
	labels := []string{"name", "index"}

	p.StatNumCommandsStart = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "commands_num_start",
			Help: "num started"},
		labels)
	err := prometheus.Register(p.StatNumCommandsStart)
	gotils.CheckFatal(err)

	p.StatLines = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "lines",
			Help: "num of lines logged"},
		labels)
	err = prometheus.Register(p.StatLines)
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
	log.SetFlags(log.Ltime | log.Lshortfile | log.Lmicroseconds | log.Ldate)

	T_START := time.Now()
	defer func() {
		log.Println("all done: dt: " + time.Since(T_START).String() + "\n")
		glog.Println("all done: dt: " + time.Since(T_START).String() + "\n")
	}()

	flag_version := flag.Bool(
		"version",
		false,
		"print the version number")

	flag_concurrency := flag.Int(
		"j",
		100,
		"num of concurrent processes")

	flag_metrics_address := flag.String(
		"metrics_address",
		"0.0.0.0:9105",
		"prometheus metrics address")

	flag_log_prefix := flag.String(
		"log_prefix",
		"_log.log",
		"rotating log files")

	flag_is_repeat := flag.Bool(
		"repeat",
		true,
		"repeat each process forever")

	flag.Parse()
	log.Println("concurrency:", *flag_concurrency)

	// configure file logging:
	if *flag_log_prefix != "" {
		glog.Default.Level = glog.INFO
		log.Println("logging to:", *flag_log_prefix, "*")
		glog.Default.Out = &writers.FixedSizeFileWriter{
			Name:     *flag_log_prefix,
			MaxSize:  1 * 1024 * 1024, // 10m
			MaxCount: 10,
		}
	}

	if *flag_version {
		log.Println(forever.VersionString())
		os.Exit(0)
	}

	p := &Forever{}
	p.ConcurrentCommands = *flag_concurrency
	p.IsRepeatForever = *flag_is_repeat
	p.worker = limiter.NewConcurrencyLimiter(p.ConcurrentCommands)
	defer p.Close()

	setupPromMetrics(p, *flag_metrics_address)
	p.Run()
}
