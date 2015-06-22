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

type Message struct {
	sserial string
	argument string
}

func main() {
	port := flag.Int("port", 6510, "which UDP port to listen on")
	n_processes := flag.Int("processes", 32, "number of processes in process pool")
	backlog := flag.Int("backlog", 32768, "how many messages can be enqueued before new messages are dropped")
	server := flag.String("server", "", "path to server; executable which the process pool will consist of")

	flag.Parse()

	if *server == "" {
		fmt.Fprintln(os.Stderr, "a server must be specified")
		os.Exit(1)
	}

	AssertFileIsExecutable(*server)

	log.Printf("[n/a] INFO starting udpool with port=%d, processes=%d, backlog=%d, server=%s", *port, *n_processes, *backlog, *server)

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
					out, err := exec.Command(*server, msg.argument).CombinedOutput()
					if err != nil {
						log.Printf("%s FAIL %s %s (%s) :: %d bytes of output: %s", msg.sserial, *server, msg.argument, err, len(out), out)
					} else {
						log.Printf("%s DONE %s %s", msg.sserial, *server, msg.argument)
					}
				case <-worker_shutdown:
					wait_group.Done()
					log.Printf("[n/a] INFO worker #%d quits", process_id)
					return
				}
			}
		}(i)
	}

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
			case queue <- message:
			default:
				log.Printf("%s DROP %s", sserial, arg)
			}

			serial++
		}
	}()

	signals := make(chan os.Signal)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGQUIT, syscall.SIGTERM)
	<-signals

	for i := 0; i < *n_processes; i++ {
		worker_shutdown <- 1
	}

	wait_group.Wait()
}
