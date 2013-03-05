//
//  Load-balancing broker.
//  Clients and workers are shown here in-process
//
package main

import (
	zmq "github.com/pebbe/zmq3"

	"fmt"
	//"math/rand"
	"time"
)

const (
	NBR_CLIENTS = 10
	NBR_WORKERS = 3
)

//  Basic request-reply client using REQ socket
//  Since Go Send and Recv can handle 0MQ binary identities we
//  don't need printable text identity to allow routing.

func client_task() {
	client, _ := zmq.NewSocket(zmq.REQ)
	defer client.Close()
	// set_id(client) //  Set a printable identity
	client.Connect("ipc://frontend.ipc")

	//  Send request, get reply
	client.Send("HELLO", 0)
	reply, _ := client.Recv(0)
	fmt.Println("Client:", reply)
}

//  While this example runs in a single process, that is just to make
//  it easier to start and stop the example.
//  This is the worker task, using a REQ socket to do load-balancing.
//  Since Go Send and Recv can handle 0MQ binary identities we
//  don't need printable text identity to allow routing.

func worker_task() {
	worker, _ := zmq.NewSocket(zmq.REQ)
	defer worker.Close()
	// set_id(worker)
	worker.Connect("ipc://backend.ipc")

	//  Tell broker we're ready for work
	worker.Send("READY", 0)

	for {
		//  Read and save all frames until we get an empty frame
		//  In this example there is only 1 but it could be more
		identity, _ := worker.Recv(0)
		empty, _ := worker.Recv(0)
		if empty != "" {
			panic(fmt.Sprintf("empty is not \"\": %q", empty))
		}

		//  Get request, send reply
		request, _ := worker.Recv(0)
		fmt.Println("Worker:", request)

		worker.Send(identity, zmq.SNDMORE)
		worker.Send("", zmq.SNDMORE)
		worker.Send("OK", 0)
	}
}

//  This is the main task. It starts the clients and workers, and then
//  routes requests between the two layers. Workers signal READY when
//  they start; after that we treat them as ready when they reply with
//  a response back to a client. The load-balancing data structure is
//  just a queue of next available workers.

func main() {
	//  Prepare our sockets
	frontend, _ := zmq.NewSocket(zmq.ROUTER)
	backend, _ := zmq.NewSocket(zmq.ROUTER)
	defer frontend.Close()
	defer backend.Close()
	frontend.Bind("ipc://frontend.ipc")
	backend.Bind("ipc://backend.ipc")

	client_nbr := 0
	for ; client_nbr < NBR_CLIENTS; client_nbr++ {
		go client_task()
	}
	for worker_nbr := 0; worker_nbr < NBR_WORKERS; worker_nbr++ {
		go worker_task()
	}

	//  Here is the main loop for the least-recently-used queue. It has two
	//  sockets; a frontend for clients and a backend for workers. It polls
	//  the backend in all cases, and polls the frontend only when there are
	//  one or more workers ready. This is a neat way to use 0MQ's own queues
	//  to hold messages we're not ready to process yet. When we get a client
	//  reply, we pop the next available worker, and send the request to it,
	//  including the originating client identity. When a worker replies, we
	//  re-queue that worker, and we forward the reply to the original client,
	//  using the reply envelope.

	//  Queue of available workers
	worker_queue := make([]string, 0, 10)

	pool := func(soc *zmq.Socket, ch chan string) {
		for {
			msg, err := soc.Recv(0)
			if err != nil {
				panic(err)
			}
			ch <- msg
		}
	}
	chBack := make(chan string, 100)
	chFrontIf := make(chan string, 100)
	go pool(backend, chBack)
	go pool(frontend, chFrontIf)

	chNil := make(chan string) // a channel that is never used
	for client_nbr > 0 {
		chFront := chNil
		//  Poll frontend only if we have available workers
		if len(worker_queue) > 0 {
			chFront = chFrontIf
		}
		select {
		case msg := <-chBack: //  Handle worker activity on backend
			//  Queue worker identity for load-balancing
			worker_id := msg
			if !(len(worker_queue) < NBR_WORKERS) {
				panic("!(len(worker_queue) < NBR_WORKERS)")
			}
			worker_queue = append(worker_queue, worker_id)

			//  Second frame is empty
			empty := <-chBack
			if empty != "" {
				panic(fmt.Sprintf("empty is not \"\": %q", empty))
			}

			//  Third frame is READY or else a client reply identity
			client_id := <-chBack

			//  If client reply, send rest back to frontend
			if client_id != "READY" {
				empty := <-chBack
				if empty != "" {
					panic(fmt.Sprintf("empty is not \"\": %q", empty))
				}
				reply := <-chBack
				frontend.Send(client_id, zmq.SNDMORE)
				frontend.Send("", zmq.SNDMORE)
				frontend.Send(reply, 0)
				client_nbr--
			}
		case msg := <-chFront: //  Here is how we handle a client request:
			//  Now get next client request, route to last-used worker
			//  Client request is [identity][empty][request]
			client_id := msg
			empty := <-chFront
			if empty != "" {
				panic(fmt.Sprintf("empty is not \"\": %q", empty))
			}
			request := <-chFront

			backend.Send(worker_queue[0], zmq.SNDMORE)
			backend.Send("", zmq.SNDMORE)
			backend.Send(client_id, zmq.SNDMORE)
			backend.Send("", zmq.SNDMORE)
			backend.Send(request, 0)

			//  Dequeue and drop the next worker identity
			worker_queue = worker_queue[1:]
		}
	}

	time.Sleep(100 * time.Millisecond)
}

/*
func set_id(soc *zmq.Socket) {
	identity := fmt.Sprintf("%04X-%04X", rand.Intn(0x10000), rand.Intn(0x10000))
	soc.SetIdentity(identity)
}
*/
