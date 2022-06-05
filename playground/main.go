package main

import (
	"bufio"
	"flag"
	"log"
	"os"
	"time"

	"github.com/korovkin/forever"
)

func main() {
	log.SetFlags(log.Ltime | log.Lshortfile | log.Lmicroseconds | log.Ldate)

	T_START := time.Now()
	defer func() {
		log.Println("all done: dt: " + time.Since(T_START).String())
		log.Println("all done: dt: " + time.Since(T_START).String())
	}()

	flag_version := flag.Bool(
		"version",
		false,
		"print the version number")

	flag_concurrency := flag.Int(
		"j",
		100,
		"num of concurrent processes")

	flag_log_prefix := flag.String(
		"log_prefix",
		"_log.log",
		"rotating log files")

	flag.Parse()
	log.Println("concurrency:", *flag_concurrency)

	// configure file logging:
	if *flag_log_prefix != "" {
		log.Println("logging to:", *flag_log_prefix, "*")
	}

	if *flag_version {
		log.Println(forever.VersionString())
		os.Exit(0)
	}

	fileScanner := bufio.NewScanner(os.Stdin)
	fileScanner.Split(bufio.ScanLines)

	for fileScanner.Scan() {
		line := fileScanner.Text()

		if len(line) > 2 {
			for line[len(line)-1] == 92 && fileScanner.Scan() {
				l := fileScanner.Text()
				// log.Println("line:", line, "l:", l)
				line = line[0 : len(line)-1]
				line += l
			}
			log.Println("line:", line)
		}
	}
}
