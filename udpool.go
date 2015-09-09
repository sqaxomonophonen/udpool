package main

import "fmt"
import "os"
import "os/signal"
import "flag"
import "net"
import "os/exec"
import "syscall"
import "log"
import "time"
import "strings"
import "strconv"
import "sync"

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

func GetSignalNameMap() (map[string]syscall.Signal) {
	return map[string]syscall.Signal {
		"HUP": syscall.SIGHUP,
		"INT": syscall.SIGINT,
		"QUIT": syscall.SIGQUIT,
		"ILL": syscall.SIGILL,
		"ABRT": syscall.SIGABRT,
		"FPE": syscall.SIGFPE,
		"KILL": syscall.SIGKILL,
		"USR1": syscall.SIGUSR1,
		"SEGV": syscall.SIGSEGV,
		"USR2": syscall.SIGUSR2,
		"PIPE": syscall.SIGPIPE,
		"ALRM": syscall.SIGALRM,
		"TERM": syscall.SIGTERM,
	}
}

func GetSignalForSignalName(name string) (syscall.Signal) {
	return GetSignalNameMap()[name]
}

func GetSignalNameForSignal(signal syscall.Signal) (string) {
	for k,v := range GetSignalNameMap() {
		if signal == v {
			return k
		}
	}
	return "????"
}

type TimeoutAction struct {
	duration time.Duration
	signal syscall.Signal
	signal_name string
}

func ParseTimeoutArgument(arg string) ([]TimeoutAction) {
	if arg == "" {
		return []TimeoutAction {}
	}

	var e interface{}

	action_strings := strings.Split(arg, "/")
	ret := make([]TimeoutAction, len(action_strings))
	for i,action_string := range action_strings {
		parts := strings.Split(action_string, ":")
		ret[i].duration, e = time.ParseDuration(parts[0])
		if e != nil {
			fmt.Fprintln(os.Stderr, "invalid timeout string '%s'", parts[0])
			os.Exit(1)
		}

		if len(parts) == 1 {
			ret[i].signal = syscall.SIGTERM
			ret[i].signal_name = GetSignalNameForSignal(ret[i].signal)
		} else if len(parts) == 2 {
			n := parts[1]
			var si uint64
			si, e := strconv.ParseUint(n, 10, 8)
			if e == nil {
				ret[i].signal = syscall.Signal(si)
				ret[i].signal_name = GetSignalNameForSignal(ret[i].signal)
			} else {
				n = strings.ToUpper(n)
				if n[0:3] == "SIG" {
					n = n[3:]
				}
				ret[i].signal_name = n
				ret[i].signal = GetSignalForSignalName(ret[i].signal_name)

			}
		} else {
			fmt.Fprintln(os.Stderr, "invalid timeout string '%s'", parts[0])
			os.Exit(1)
		}
	}

	return ret
}

func main() {
	port := flag.Int("port", 6510, "which UDP port to listen on")
	n_processes := flag.Int("processes", 32, "number of processes in process pool")
	backlog := flag.Int("backlog", 32768, "how many messages can be enqueued before new messages are dropped")
	server := flag.String("server", "", "path to server; executable which the process pool will consist of")
	process_timeout := flag.String("timeout", "",
			"Timeout signal actions. Default is no action; i.e. to never send any signals to child processes. " +
			"Actions are separated by '/'. The format of each action is 'duration[:signal]'. Duration format " +
			"is documented here: http://golang.org/pkg/time/#ParseDuration. Default signal is SIGTERM. " +
			"Both signal number and signal name, with or without SIG- prefix, are allowed, i.e. " +
			"'1h', '1h:SIGTERM, '1h:TERM' and '1h:15' all mean the same thing; wait one hour for the process " +
			"to finish, then send SIGTERM. Actions are executed sequentially, e.g. '1h/1m:9' means " +
			"\"wait one hour, then send SIGTERM, then wait 1 minute, then send SIGKILL\"")

	flag.Parse()

	timeout_actions := ParseTimeoutArgument(*process_timeout)

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

	queue := make(chan Message, *backlog)

	var child_process_group sync.WaitGroup

	for i := 0; i < *n_processes; i++ {
		child_process_group.Add(1)
		go func(process_id int) {
			defer child_process_group.Done()

			log.Printf("[n/a] INFO worker #%d starts", process_id)

			for {
				msg, ok := <-queue

				if !ok {
					log.Printf("[n/a] INFO worker #%d quits", process_id)
					return
				}

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
				Exit: for {
					for _,timeout_action := range timeout_actions {
						select {
						case exit_status = <-exit_status_chan:
							break Exit
						case <-time.After(timeout_action.duration):
							log.Printf("%s %s %s %s", msg.sserial, timeout_action.signal_name, *server, msg.argument)
							cmd.Process.Signal(timeout_action.signal)
						}
					}
					exit_status = <-exit_status_chan
					break Exit
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
			log.Printf("%s READ %s", sserial, arg)

			message := Message{ sserial: sserial, argument: arg }

			select {
			case <-drop_chan:
				log.Printf("%s DROP %s (reason: quitting)", sserial, arg)
				drop_chan <- 1
				continue
			default:
			}

			select {
			case queue <-message:
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

	close(queue)

	child_process_group.Wait()
}
