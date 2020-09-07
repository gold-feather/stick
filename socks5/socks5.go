package socks5

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
)

type METHOD byte
type CMD byte
type ADDRTYPE byte

//只定义了两个验证方式，其他的懒得实现
const (
	SOCKS_VERSION              byte     = 0x05
	NO_AUTHENTICATION_REQUIRED METHOD   = 0x00
	USERNAME_PASSWORD          METHOD   = 0x02
	NO_ACCEPTABLE_METHODS      METHOD   = 0xff
	CONNECT                    CMD      = 0x01
	IPV4_ADDRESS               ADDRTYPE = 0x01
	IPV6_ADDRESS               ADDRTYPE = 0x04
	DOMAIN_ADDRESS             ADDRTYPE = 0x03
)

var (
	ERR_SOCKS_VERSION_MISMATCH = errors.New("socks version isn't 5")
	ERR_READ_METHODS           = errors.New("read METHODS failed")
	ERR_UNKNOWN_ADDRESS_TYPE   = errors.New("unknown address type")
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

func (s Server) auth(conn net.Conn, method METHOD) bool {
	switch method {
	//TODO: 实现USERNAME_PASSWORD
	case USERNAME_PASSWORD:
		_, err := conn.Write([]byte{SOCKS_VERSION, byte(NO_AUTHENTICATION_REQUIRED)})
		if err != nil {
			panic(err)
		}
		return false
	case NO_AUTHENTICATION_REQUIRED:
		_, err := conn.Write([]byte{SOCKS_VERSION, byte(NO_AUTHENTICATION_REQUIRED)})
		if err != nil {
			panic(err)
		}
		return true
	default: //NO_AUTHENTICATION_REQUIRED放在这里处理
		_, err := conn.Write([]byte{SOCKS_VERSION, byte(NO_AUTHENTICATION_REQUIRED)})
		if err != nil {
			panic(err)
		}
		return false
	}
}

func (s Server) handleConnection(conn net.Conn) {
	defer conn.Close()
	method := s.chooseMethod(conn)
	if !s.auth(conn, method) {
		return
	}
	request := s.getRequest(conn)
	_ = request
}

type request struct {
	cmd      CMD
	addrType ADDRTYPE
	addr     interface{}
	port     uint16
}

func (s Server) getRequest(conn net.Conn) request {
	req := request{}
	//VER | CMD |  RSV
	header := make([]byte, 3, 3)
	_, err := io.ReadFull(conn, header)
	if err != nil {
		panic(err)
	}
	if header[0] != SOCKS_VERSION {
		panic(fmt.Errorf("%w: %v", ERR_SOCKS_VERSION_MISMATCH, header[0]))
	}
	req.cmd = CMD(header[1])
	addrType := make([]byte, 1, 1)
	_, err = io.ReadFull(conn, addrType)
	if err != nil {
		panic(err)
	}
	req.addrType = ADDRTYPE(addrType[0])
	switch req.addrType {
	case IPV4_ADDRESS:
		addr := make([]byte, 4, 4)
		_, err := io.ReadFull(conn, addr)
		if err != nil {
			panic(err)
		}
		req.addr = net.IP(addr)
	case IPV6_ADDRESS:
		addr := make([]byte, 16, 16)
		_, err := io.ReadFull(conn, addr)
		if err != nil {
			panic(err)
		}
		req.addr = net.IP(addr)
	case DOMAIN_ADDRESS:
		addrLen := make([]byte, 1, 1)
		_, err := io.ReadFull(conn, addrLen)
		if err != nil {
			panic(err)
		}
		addr := make([]byte, addrLen[0])
		_, err = io.ReadFull(conn, addr)
		if err != nil {
			panic(err)
		}
		req.addr = string(addr)
	default:
		panic(fmt.Errorf("%w: %v", ERR_UNKNOWN_ADDRESS_TYPE, req.addrType))
	}
	portByte := make([]byte, 2)
	_, err = io.ReadFull(conn, portByte)
	if err != nil {
		panic(err)
	}
	req.port = binary.BigEndian.Uint16(portByte)

	return req
}
