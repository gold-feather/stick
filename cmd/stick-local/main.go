package main

import (
	"bytes"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"stick/model/transport"
	"stick/socks5"
	"sync"
	"sync/atomic"
	"time"

	"github.com/golang/protobuf/proto"

	"github.com/gorilla/websocket"
)

func main() {
	serverInfo := serverInfo{
		addr:  "",
		port:  0,
		token: "",
	}
	stickLocal := newStickLocal(serverInfo)
	go stickLocal.run()

	s5req2ncReq := func(request socks5.Request) *transport.NewConnect {
		var addrType transport.NewConnect_AddrType
		switch request.AddrType {
		case socks5.DOMAIN_ADDRESS:
			addrType = transport.NewConnect_Domain
		case socks5.IPV4_ADDRESS:
			addrType = transport.NewConnect_IPV4
		case socks5.IPV6_ADDRESS:
			addrType = transport.NewConnect_IPV6
		}
		return &transport.NewConnect{
			AddrType: addrType,
			Address:  request.Addr,
			Port:     int32(request.Port),
		}
	}

	handleConnectCMD := func(conn net.Conn, request socks5.Request) error {
		//TODO: 应该先和server连接，看情况返回的，这里直接返回成功了
		/*
			+-----+-----+-------+------+----------+----------+
			| VER | REP |  RSV  | ATYP | BND.ADDR | BND.PORT |
			+-----+-----+-------+------+----------+----------+
			|  1  |  1  | X'00' |  1   | Variable |    2     |
			+-----+-----+-------+------+----------+----------+
		*/
		//每个socks5的连接都对应一个 remoteConn
		remoteConn := stickLocal.getRemoteConn(s5req2ncReq(request))
		conn.Write([]byte{0x05, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00})

		go io.Copy(remoteConn, conn)
		io.Copy(conn, remoteConn)
		return nil
	}
	socks5server := socks5.NewServer(net.IPv4(127, 0, 0, 1), 8888,
		map[socks5.CMD]socks5.HandleCMDFunc{
			socks5.CONNECT: handleConnectCMD,
		})
	err := socks5server.Run()
	fmt.Println(err)
}

type serverInfo struct {
	addr  string
	port  byte
	token string
}

type stickLocal struct {
	server  serverInfo
	wsConn  *websocket.Conn
	connMap sync.Map
	idCount uint64
}

func newStickLocal(info serverInfo) *stickLocal {
	return &stickLocal{
		server: info,
	}
}

func (local *stickLocal) newId() uint64 {
	return atomic.AddUint64(&local.idCount, 1)
}

func (local *stickLocal) connect() {
	//TODO: 至少把path弄成可设置的
	u := url.URL{Scheme: "wss", Host: local.server.addr, Path: "/tt"}
	c, _, err := websocket.DefaultDialer.Dial(u.String(),
		http.Header{
			"token": []string{local.server.token},
		})
	if err != nil {
		panic(err)
	}
	local.wsConn = c
}

func (local *stickLocal) getRemoteConn(newConnect *transport.NewConnect) *remoteConn {
	id := local.newId()
	bytes, err := proto.Marshal(newConnect)
	if err != nil {
		log.Println(err)
		return nil
	}
	remoteConn := newRemoteConn(local, id)
	//这里提前存到map中而不是等server返回对newConnect的回复，因为run中需要从map获取remoteConn，才能知道要把这个回复传给谁
	local.connMap.Store(id, remoteConn)
	remoteConn.Write(bytes)
	var ncResp *transport.NewConnectResponse
	t := time.After(time.Second)
	select {
	case ncResp = <-remoteConn.connectInfoChan:
		if ncResp.Ojbk {
			return remoteConn
		} else {
			log.Println("not ojbk")
			return nil
		}
	case <-t:
		return nil
	}
}

func (local *stickLocal) send(message *transport.Message) error {
	bytes, err := proto.Marshal(message)
	if err != nil {
		return err
	}
	return local.wsConn.WriteMessage(websocket.BinaryMessage, bytes)
}

func (local *stickLocal) get() (*transport.Message, error) {
	messageType, bytes, err := local.wsConn.ReadMessage()
	if messageType != websocket.BinaryMessage {
		log.Printf("msgType: %d", messageType)
		return nil, fmt.Errorf("")
	}
	if err != nil {
		log.Printf("err: %v", err)
		return nil, err
	}
	msg := &transport.Message{}
	err = proto.Unmarshal(bytes, msg)
	if err != nil {
		return nil, err
	}
	return msg, nil
}

func (local *stickLocal) run() {
	local.connect()
	for {
		msg, err := local.get()
		if err != nil {
			log.Println(err)
			break
		}
		connId := msg.Id
		//读出server发来的数据，找到对应的remoteConn，直接发过去
		if v, ok := local.connMap.Load(connId); ok {
			conn := v.(*remoteConn)
			switch msg.Type {
			case transport.Message_Data:
				_, err := conn.readBf.Write(msg.Data)
				if err != nil {
					conn.Close()

				}
			case transport.Message_NewConnectResponse:
				var ncResp transport.NewConnectResponse
				err := proto.Unmarshal(msg.Data, &ncResp)
				if err != nil {
					log.Println(err)
				} else {
					conn.connectInfoChan <- &ncResp
				}
			}
		} else {
			//TODO：服务器返回不存在的连接id
		}
	}
}

type remoteConn struct {
	sync.RWMutex
	isClose         bool
	stickLocal      *stickLocal
	id              uint64
	connectInfoChan chan *transport.NewConnectResponse
	readBf          *bytes.Buffer
}

func newRemoteConn(local *stickLocal, id uint64) *remoteConn {
	buffer := bytes.NewBuffer(make([]byte, 0, 1024))
	return &remoteConn{
		stickLocal:      local,
		id:              id,
		connectInfoChan: make(chan *transport.NewConnectResponse),
		readBf:          buffer,
	}
}

func (conn *remoteConn) Close() {
	if !conn.isClose {
		defer conn.Unlock()
		conn.Lock()
		conn.isClose = true
	}
}

func (conn *remoteConn) Write(p []byte) (int, error) {
	defer conn.RUnlock()
	conn.RLock()
	if conn.isClose {
		return 0, io.ErrClosedPipe
	}
	message := &transport.Message{
		Id:   conn.id,
		Type: transport.Message_Data,
		Data: p,
	}
	conn.stickLocal.send(message)
	return len(p), nil
}

func (conn *remoteConn) Read(p []byte) (int, error) {
	defer conn.RUnlock()
	conn.RLock()
	if conn.isClose {
		return 0, io.ErrClosedPipe
	}
	return conn.readBf.Read(p)
}
