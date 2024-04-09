package main

import (
	"context"
	"log"
	"net"
	"os"

	"etcd/code/load_balance/etcdv3"
	pb "etcd/code/load_balance/proto"

	"google.golang.org/grpc"
)

type SimpleService struct{}

const (
	Network string = "tcp"
	SerName string = "simple_grpc"
)

var Address string = "localhost:8000"

var EtcdEndpoints = []string{"localhost:2379"}

func main() {
	arguments := os.Args
	if len(arguments) > 1 {
		Address = arguments[1]
	}
	listener, err := net.Listen(Network, Address)
	if err != nil {
		log.Fatal("net.Listen err:", err)
	}

	log.Println(Address + " net.Listing...")

	grpcServer := grpc.NewServer()
	pb.RegisterSimpleServer(grpcServer, &SimpleService{})

	srv, err := etcdv3.NewServiceRegister(EtcdEndpoints, SerName, Address, 5)
	if err != nil {
		log.Fatal("register service err: %v", err)
	}

	defer srv.Close()

	err = grpcServer.Serve(listener)
	if err != nil {
		log.Fatal("grpcServer.Serve err: %v", err)
	}
}

func (s *SimpleService) Route(ctx context.Context, req *pb.SimpleRequest) (*pb.SimpleResponse, error) {
	log.Println("receive data: ", req.Data)
	res := pb.SimpleResponse{
		Code:  200,
		Value: "hello " + req.Data,
	}
	return &res, nil
}
