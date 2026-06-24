package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"sync/atomic"
	"time"

	pb "github.com/avidubey/kv-gateway/proto"
	"github.com/avidubey/kv-gateway/internal/sim"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

var (
	port        = flag.Int("port", 9090, "gRPC listen port")
	workerID    = flag.String("id", "worker-1", "unique worker identifier")
	kvCapacity  = flag.Int64("kv-capacity", 16000, "KV cache capacity in tokens")
	gatewayAddr = flag.String("gateway", "localhost:9000", "gateway gRPC address for heartbeat")
	advertise   = flag.String("advertise", "", "address gateway uses to reach this worker (default: localhost:<port>)")
	failAfter   = flag.Int("fail-after", 0, "exit after N seconds (0 = never)")
)

type server struct {
	pb.UnimplementedWorkerTelemetryServer
	kv          *sim.KVCache
	activeReqs  atomic.Int32
}

func (s *server) Generate(req *pb.GenerateRequest, stream pb.WorkerTelemetry_GenerateServer) error {
	promptTokens := sim.EstimateTokens(req.Prompt)
	totalTokens := int64(promptTokens) + int64(req.MaxTokens)

	occupancy, ok := s.kv.Reserve(totalTokens)
	if !ok {
		return fmt.Errorf("KV cache full (occupancy=%.2f), cannot reserve %d tokens", occupancy, totalTokens)
	}
	defer s.kv.Release(totalTokens)

	s.activeReqs.Add(1)
	defer s.activeReqs.Add(-1)

	prefill := sim.PrefillDelay(promptTokens, occupancy)
	time.Sleep(prefill)

	tokenDelay := sim.TokenDelay(occupancy)
	for i := int32(0); i < req.MaxTokens; i++ {
		time.Sleep(tokenDelay)
		done := i == req.MaxTokens-1
		if err := stream.Send(&pb.Token{Text: "tok ", Done: done}); err != nil {
			return err
		}
	}
	return nil
}

func (s *server) Stream(stream pb.WorkerTelemetry_StreamServer) error {
	for {
		_, err := stream.Recv()
		if err != nil {
			return err
		}
		if err := stream.Send(&pb.HeartbeatAck{Ok: true}); err != nil {
			return err
		}
	}
}

func main() {
	flag.Parse()

	if *failAfter > 0 {
		go func() {
			time.Sleep(time.Duration(*failAfter) * time.Second)
			log.Printf("worker %s: --fail-after=%ds triggered, exiting", *workerID, *failAfter)
			os.Exit(1)
		}()
	}

	if *advertise == "" {
		*advertise = fmt.Sprintf("localhost:%d", *port)
	}

	kv := sim.NewKVCache(*kvCapacity)
	srv := &server{kv: kv}

	go runHeartbeat(srv, kv)

	lis, err := net.Listen("tcp", fmt.Sprintf(":%d", *port))
	if err != nil {
		log.Fatalf("failed to listen: %v", err)
	}
	gs := grpc.NewServer()
	pb.RegisterWorkerTelemetryServer(gs, srv)
	log.Printf("worker %s listening on :%d (KV capacity=%d)", *workerID, *port, *kvCapacity)
	if err := gs.Serve(lis); err != nil {
		log.Fatalf("failed to serve: %v", err)
	}
}

func runHeartbeat(srv *server, kv *sim.KVCache) {
	for {
		func() {
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()

			conn, err := grpc.NewClient(*gatewayAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
			if err != nil {
				log.Printf("heartbeat: dial failed: %v", err)
				time.Sleep(time.Second)
				return
			}
			defer conn.Close()

			client := pb.NewWorkerTelemetryClient(conn)
			stream, err := client.Stream(ctx)
			if err != nil {
				log.Printf("heartbeat: stream failed: %v", err)
				time.Sleep(time.Second)
				return
			}

			ticker := time.NewTicker(100 * time.Millisecond)
			defer ticker.Stop()

			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					used, capacity := kv.Stats()
					active := srv.activeReqs.Load()
					tps := 0.0
					if active > 0 {
						tps = float64(active) * 100.0 // rough estimate
					}
					err := stream.Send(&pb.HeartbeatRequest{
						WorkerId:        *workerID + "@" + *advertise,
						KvUsedTokens:    used,
						KvCapacityTokens: capacity,
						ActiveRequests:  active,
						TokensPerSec:    tps,
					})
					if err != nil {
						log.Printf("heartbeat: send failed: %v", err)
						return
					}
					_, err = stream.Recv()
					if err != nil {
						log.Printf("heartbeat: recv failed: %v", err)
						return
					}
				}
			}
		}()
		time.Sleep(time.Second)
	}
}
