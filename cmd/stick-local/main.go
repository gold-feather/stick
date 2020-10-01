package main

import (
	"fmt"
	"net"
	"stick/socks5"
)

func main() {
	server := socks5.NewServer(net.IPv4(127, 0, 0, 1), 8888, map[socks5.CMD]socks5.HandleCMDFunc{})
	err := server.Run()
	fmt.Println(err)
}
