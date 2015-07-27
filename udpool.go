package main

import "fmt"
import "os"
import "os/signal"
import "flag"
import "net"
import "sync"
import "os/exec"
import "syscall"
import "log"
import "time"
import "strings"

func AssertFileIsExecutable(path string) {
	file, err := os.Open(path)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fileinfo, err := file.Stat()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if fileinfo.Mode() & 0111 == 0 {
		fmt.Fprintln(os.Stderr, "server is not executable\n")
		os.Exit(1)
	}
	file.Close()
}

func ParseTimeoutArgument(process_timeout string) (bool, [2]time.Duration) {
	process_timeout_enabled := false

	var sigterm_timeout time.Duration
	var sigkill_timeout time.Duration

	if process_timeout != "" {
		parts := strings.Split(process_timeout, "/")
		if len(parts) < 1 || len(parts) > 2 {
			fmt.Fprintln(os.Stderr, "invalid timeout")
			os.Exit(1)
		}

		var e error

		sigterm_timeout, e = time.ParseDuration(parts[0])
		if e != nil {
			fmt.Fprintln(os.Stderr, "invalid sigterm timeout")
			os.Exit(1)
		}

		var sigkill_timeout_string string
		sigkill_timeout_string = "1m"
		if len(parts) == 2 {
			sigkill_timeout_string = parts[1]
		}

		sigkill_timeout, e = time.ParseDuration(sigkill_timeout_string)
		if e != nil {
			fmt.Fprintln(os.Stderr, "invalid sigkill timeout")
			os.Exit(1)
		}

		process_timeout_enabled = true
	}

	return process_timeout_enabled, [2]time.Duration{sigterm_timeout, sigkill_timeout}
}

func main() {
	port := flag.Int("port", 6510, "which UDP port to listen on")
	n_processes := flag.Int("processes", 32, "number of processes in process pool")
	backlog := flag.Int("backlog", 32768, "how many messages can be enqueued before new messages are dropped")
	server := flag.String("server", "", "path to server; executable which the process pool will consist of")
	process_timeout := flag.String("timeout", "", "process timeout duration. e.g. '2h' " +
		"(wait 2 hours, then SIGTERM, wait 1 minute, then SIGKILL) or '1m/1s' " +
		"(wait 1 min, then SIGTERM, wait 1 second, then SIGKILL). " +
		"Duration format is documented here: http://golang.org/pkg/time/#ParseDuration")

	flag.Parse()

	process_timeout_enabled, process_timeout_durations := ParseTimeoutArgument(*process_timeout)
	signals := [2]syscall.Signal{syscall.SIGTERM, syscall.SIGKILL}
	signal_names := [2]string{"TERM", "KILL"}

	if *server == "" {
		fmt.Fprintln(os.Stderr, "a server must be specified")
		os.Exit(1)
	}

	AssertFileIsExecutable(*server)

	log.Printf("[n/a] INFO starting udpool with port=%d, processes=%d, backlog=%d, server=%s", *port, *n_processes, *backlog, *server)

	type Message struct {
		sserial string
		argument string
	}

	var wait_group sync.WaitGroup
	queue := make(chan Message, *backlog)
	worker_shutdown := make(chan int)

	for i := 0; i < *n_processes; i++ {
		wait_group.Add(1)
		go func(process_id int) {
			log.Printf("[n/a] INFO worker #%d starts", process_id)
			for {
				select {
				case msg := <-queue:
					log.Printf("%s EXEC %s %s", msg.sserial, *server, msg.argument)
					cmd := exec.Command(*server, msg.argument)

					type ExitStatus struct {
						state os.ProcessState
						output []byte
						err error
					}

					exit_status_chan := make(chan ExitStatus)

					go func() {
						output, err := cmd.CombinedOutput()
						exit_status_chan <- ExitStatus { state: *cmd.ProcessState, output: output, err: err }
					}()

					var exit_status ExitStatus
					if process_timeout_enabled {
						Exit:
						for {
							for i := 0; i < 2; i++ {
								select {
								case exit_status = <-exit_status_chan:
									break Exit
								case <-time.After(process_timeout_durations[i]):
									log.Printf("%s %s %s %s", msg.sserial, signal_names[i], *server, msg.argument)
									cmd.Process.Signal(signals[i])
								}
							}
							exit_status = <-exit_status_chan
							break Exit
						}
					} else {
						exit_status = <-exit_status_chan
					}

					if exit_status.state.Success() {
						log.Printf("%s DONE %s %s", msg.sserial, *server, msg.argument)
					} else {
						log.Printf(
							"%s FAIL %s %s (%s) :: %d bytes of output: %s",
							msg.sserial,
							*server,
							msg.argument,
							exit_status.err,
							len(exit_status.output),
							exit_status.output)
					}
				case <-worker_shutdown:
					wait_group.Done()
					log.Printf("[n/a] INFO worker #%d quits", process_id)
					return
				}
			}
		}(i)
	}

	drop_chan := make(chan int, 1)

	go func() {
		conn, err := net.ListenUDP("udp", &net.UDPAddr{Port: *port})
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}

		serial := 1

		b := make([]byte, 32768)
		for {
			sserial := fmt.Sprintf("[s:%08x]", serial)
			rd, err := conn.Read(b)
			if err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(1)
			}
			arg := string(b[:rd])
			message := Message{ sserial: sserial, argument: arg }
			log.Printf("%s ACPT %s", sserial, arg)
			select {
			case <-drop_chan:
				log.Printf("%s DROP %s (reason: quitting)", sserial, arg)
				drop_chan <- 1
			case queue <- message:
				// (handled by receiver of 'queue')
			default:
				log.Printf("%s DROP %s (reason: queue is full)", sserial, arg)
			}

			serial++
		}
	}()

	signal_chan := make(chan os.Signal)
	signal.Notify(signal_chan, syscall.SIGINT, syscall.SIGQUIT, syscall.SIGTERM)
	<-signal_chan

	drop_chan <- 1

	for i := 0; i < *n_processes; i++ {
		worker_shutdown <- 1
	}

	wait_group.Wait()
}
