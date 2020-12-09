package socks5

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"stick/object"

	"go.uber.org/zap"
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
	NOT_SUPPORT_CMD_RESP = []byte{0x05, 0x07, 0x00, 0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00}

	logger = object.GetLogger()
)

type HandleCMDFunc func(conn net.Conn, request Request) error

type Server struct {
	ip               string
	port             uint16
	handleConnectCMD HandleCMDFunc
}

func NewServer(ip string, port uint16, handleCMDFuncMap map[CMD]HandleCMDFunc) Server {
	var handleConnectCMD HandleCMDFunc
	if f, ok := handleCMDFuncMap[CONNECT]; ok {
		handleConnectCMD = f
	} else {
		handleConnectCMD = handleConnectCMDLocal
	}
	return Server{
		ip:               ip,
		port:             port,
		handleConnectCMD: handleConnectCMD,
	}
}

func (s Server) Run() error {
	listener, err := net.Listen("tcp", fmt.Sprintf("%s:%d", s.ip, s.port))
	if err != nil {
		return err
	}
	for {
		conn, err := listener.Accept()
		if err != nil {
			logger.Error("accept conn err", zap.Error(err))
			continue
		}
		go func() {
			//defer func() {
			//	if err := recover(); err != nil {
			//		log.Printf("%+v", err)
			//	}
			//}()
			s.handleConnection(conn)
		}()
	}
}

func (s Server) chooseMethod(conn net.Conn) METHOD {
	VERandNMETHODS := make([]byte, 2)
	if _, err := io.ReadFull(conn, VERandNMETHODS); err != nil {
		panic(fmt.Errorf("read VER and NMETHODS fail, err: %w", err))
	} else if VERandNMETHODS[0] != SOCKS_VERSION {
		panic(fmt.Errorf("%w: %v", ERR_SOCKS_VERSION_MISMATCH, VERandNMETHODS[0]))
	} else {
		nMethods := int(VERandNMETHODS[1])
		methods := make([]byte, nMethods)
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
	switch request.CMD {
	case CONNECT:
		s.handleConnectCMD(conn, request)
	default:
		conn.Write(NOT_SUPPORT_CMD_RESP)
		return
	}
}

type Request struct {
	CMD      CMD
	AddrType ADDRTYPE
	Addr     string
	Port     uint16
}

func (s Server) getRequest(conn net.Conn) Request {
	req := Request{}
	//VER | CMD |  RSV
	header := make([]byte, 3)
	_, err := io.ReadFull(conn, header)
	if err != nil {
		panic(err)
	}
	logger.Info("get request header", zap.String("header", fmt.Sprint(header)), zap.String("ip", conn.RemoteAddr().String()))
	if header[0] != SOCKS_VERSION {
		panic(fmt.Errorf("%w: %v", ERR_SOCKS_VERSION_MISMATCH, header[0]))
	}
	req.CMD = CMD(header[1])
	addrType := make([]byte, 1)
	_, err = io.ReadFull(conn, addrType)
	if err != nil {
		panic(err)
	}
	req.AddrType = ADDRTYPE(addrType[0])
	switch req.AddrType {
	case IPV4_ADDRESS:
		addr := make([]byte, 4)
		_, err := io.ReadFull(conn, addr)
		if err != nil {
			panic(err)
		}
		req.Addr = net.IP(addr).String()
	case IPV6_ADDRESS:
		addr := make([]byte, 16)
		_, err := io.ReadFull(conn, addr)
		if err != nil {
			panic(err)
		}
		req.Addr = net.IP(addr).String()
	case DOMAIN_ADDRESS:
		addrLen := make([]byte, 1)
		_, err := io.ReadFull(conn, addrLen)
		if err != nil {
			panic(err)
		}
		addr := make([]byte, addrLen[0])
		_, err = io.ReadFull(conn, addr)
		if err != nil {
			panic(err)
		}
		req.Addr = string(addr)
	default:
		panic(fmt.Errorf("%w: %v", ERR_UNKNOWN_ADDRESS_TYPE, req.AddrType))
	}
	portByte := make([]byte, 2)
	_, err = io.ReadFull(conn, portByte)
	if err != nil {
		panic(err)
	}
	req.Port = binary.BigEndian.Uint16(portByte)

	logger.Info("get req", zap.Any("req", req))
	return req
}

func handleConnectCMDLocal(conn net.Conn, request Request) error {
	conn.Write([]byte{0x05, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00})
	var addrIP string
	switch request.AddrType {
	case DOMAIN_ADDRESS:
		ipAddr, err := net.ResolveIPAddr("ip", request.Addr)
		if err != nil {
			panic(err)
		}
		addrIP = ipAddr.IP.String()
	case IPV4_ADDRESS, IPV6_ADDRESS:
		addrIP = request.Addr
	}

	reqConn, err := net.Dial("tcp", fmt.Sprintf("%s:%d", addrIP, request.Port))
	if err != nil {
		panic(err)
	}
	go io.Copy(reqConn, conn)
	io.Copy(conn, reqConn)
	return nil
}
