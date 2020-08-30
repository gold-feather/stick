package socks5

import (
	"errors"
	"fmt"
	"io"
	"log"
	"net"
)

type METHOD int

//只定义了两个验证方式，其他的懒得实现
const (
	SOCKS_VERSION              byte   = 0x05
	NO_AUTHENTICATION_REQUIRED METHOD = 0x00
	USERNAME_PASSWORD          METHOD = 0x02
	NO_ACCEPTABLE_METHODS      METHOD = 0xff
)

var (
	ERR_SOCKS_VERSION_MISMATCH = errors.New("socks version isn't 5")
	ERR_READ_METHODS           = errors.New("read METHODS failed")
	METHOD_MAP                 = map[byte]METHOD{
		0x00: NO_AUTHENTICATION_REQUIRED,
		0x02: USERNAME_PASSWORD,
	}
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
		go func() {
			defer func() {
				if err := recover(); err != nil {
					log.Println(err)
				}
			}()
			s.handleConnection(conn)
		}()
	}
}

func (s Server) chooseMethod(conn net.Conn) METHOD {
	VERandNMETHODS := make([]byte, 2, 2)
	if _, err := io.ReadFull(conn, VERandNMETHODS); err != nil {
		panic(fmt.Errorf("read VER and NMETHODS fail, err: %w", err))
	} else if VERandNMETHODS[0] != SOCKS_VERSION {
		panic(fmt.Errorf("%w: %v", ERR_SOCKS_VERSION_MISMATCH, VERandNMETHODS[0]))
	} else {
		nMethods := int(VERandNMETHODS[1])
		methods := make([]byte, nMethods, nMethods)
		if _, err := io.ReadFull(conn, methods); err != nil {
			panic(ERR_READ_METHODS)
		} else {
			passwordMethodFlag := false
			for _, methodByte := range methods {
				//优先不用密码
				if methodByte == byte(NO_AUTHENTICATION_REQUIRED) {
					return NO_AUTHENTICATION_REQUIRED
				} else if methodByte == byte(USERNAME_PASSWORD) {
					passwordMethodFlag = true
				}
			}
			if passwordMethodFlag {
				return USERNAME_PASSWORD
			}
		}
	}
	return NO_ACCEPTABLE_METHODS
}

func (s Server) handleConnection(conn net.Conn) {
	method := s.chooseMethod(conn)
	_ = method
}
