# udpool

A small UDP server written in `golang` which runs an executable of your choice for every message received. The executable receives your message as its first argument. Process spawning occurs in a goroutine pool so that you can limit the maximum number of simultaneous processes. If the pool has no free slots your message will be queued until a slot is free. The size of the pool and the maximum queue length are configurable. When the queue reaches its maximum length the server will start dropping your messages.

The server logs what it does and tags executions with a serial. It's intended as a poor man's journal in case something goes wrong.

When the server receives an exit signal (`SIGINT`, `SIGQUIT` or `SIGTERM`) it will wait for child processes to stop before quitting.

You can use netcat as a simple client, e.g.: `echo -n "hello" | nc -uw0 127.0.0.1 6510`

Or use a tiny C client:
```C
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/socket.h>
#include <netinet/in.h>

#define PORT (6510)

int main(int argc, char** argv)
{
	if (argc != 2) {
		fprintf(stderr, "%s <arg>\n", argv[0]);
		return EXIT_FAILURE;
	}

	int fd = socket(AF_INET, SOCK_DGRAM, 0);

	struct sockaddr_in servaddr;
	memset(&servaddr, 0, sizeof(servaddr));
	servaddr.sin_family = AF_INET;
	servaddr.sin_port = htons(PORT);

	sendto(fd, argv[1], strlen(argv[1]), 0, (struct sockaddr*)&servaddr, sizeof(servaddr));

	return EXIT_SUCCESS;
}

```

`go build` is all you need to build it. No dependencies.

All code is cc0 / public domain.

Developed for internal use at my work at https://github.com/ColourboxDevelopment/ - it was written because `xinetd` does not solve this problem; it only supports single-threaded UDP (no simultaneous requests) and is designed to send output from the executable back to the sender. A server using Amazon SQS was also considered instead of UDP but was considered too bloated on the client-side.
