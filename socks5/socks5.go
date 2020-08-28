package socks5

import (
	"log"
	"net"
)

type Server struct {
	ip     net.IP
	port   uint16
	handle func(conn net.Conn)
}

func (s Server) run() error {
	listener, err := net.Listen("tcp", string(s.ip))
	if err != nil {
		return nil
	}
	for {
		conn, err := listener.Accept()
		if err != nil {
			log.Println(err)
			continue
		}
		go s.handleConnection(conn)
	}
}

func (s Server) handleConnection(conn net.Conn) {

}
