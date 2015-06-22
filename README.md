# udpool

A small `golang` UDP server that passes incoming messages to a process pool. Each message causes an executable
of your choice to be executed with your message as its first argument. When the process pool has no free slots
your message will be queued, unless the queue itself is also full, in which case the message is dropped.
The sizes of the process pool and your queue can be passed as command-line options. The server logs everything
it does to `stderr`. The output from your executable is logged only if it returns with a non-zero exit status.

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

All code is cc0 / public domain.

