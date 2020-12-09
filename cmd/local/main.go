package main

import (
	"bytes"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"runtime"
	"stick/model/transport"
	"stick/socks5"
	"sync"
	"sync/atomic"
	"time"

	"go.uber.org/zap"

	"stick/object"

	"github.com/golang/protobuf/proto"

	"github.com/gorilla/websocket"
)

var (
	logger     = object.GetLogger()
	serverAddr = flag.String("server", "", "服务器地址")
	token      = flag.String("token", "", "密码")
	socks5Addr = flag.String("sock5addr", "127.0.0.1", "sock5监听ip")
	socks5Port = flag.Int("socks5port", 8888, "socks5监听port")
)

func init() {
	flag.Parse()
}

func main() {
	if serverAddr == nil || len(*serverAddr) == 0 {
		logger.Fatal("need -server")
	}
	serverInfo := serverInfo{
		addr:  *serverAddr,
		token: *token,
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

		logger.Info("get remoteConn", zap.Uint64("id", remoteConn.id))
		f := func(w io.Writer, r io.Reader) {
			n, err := io.Copy(w, r)
			logger.Info("copy end", zap.Int64("len", n), zap.Error(err))
		}
		go f(remoteConn, conn)
		f(conn, remoteConn)
		return nil
	}
	socks5server := socks5.NewServer(*socks5Addr, uint16(*socks5Port),
		map[socks5.CMD]socks5.HandleCMDFunc{
			socks5.CONNECT: handleConnectCMD,
		})
	err := socks5server.Run()
	fmt.Println(err)
}

type serverInfo struct {
	addr  string
	token string
}

type stickLocal struct {
	server       serverInfo
	wsConn       *websocket.Conn
	wsWriteMutex sync.Mutex
	connMap      sync.Map
	idCount      uint64
}

func newStickLocal(info serverInfo) *stickLocal {
	return &stickLocal{
		server: info,
	}
}

func (local *stickLocal) newId() uint64 {
	return atomic.AddUint64(&local.idCount, 1)
}

func (local *stickLocal) writeMessage(msg []byte) error {
	local.wsWriteMutex.Lock()
	defer local.wsWriteMutex.Unlock()
	return local.wsConn.WriteMessage(websocket.BinaryMessage, msg)
}

func (local *stickLocal) connect() {
	//TODO: 至少把path弄成可设置的
	u := url.URL{Scheme: "ws", Host: local.server.addr, Path: "/tt"}
	c, _, err := websocket.DefaultDialer.Dial(u.String(),
		http.Header{
			"token": []string{local.server.token},
		})
	if err != nil {
		panic(err)
	}
	local.wsConn = c
	logger.Info("connect to server success")
}

func (local *stickLocal) getRemoteConn(newConnect *transport.NewConnect) *remoteConn {
	id := local.newId()
	newConnReqBytes, _ := proto.Marshal(newConnect)
	msg := &transport.Message{
		Id:   id,
		Type: transport.Message_NewConnect,
		Data: newConnReqBytes,
	}
	msgBytes, _ := proto.Marshal(msg)
	remoteConn := newRemoteConn(local, id)
	//这里提前存到map中而不是等server返回对newConnect的回复，因为run中需要从map获取remoteConn，才能知道要把这个回复传给谁
	local.connMap.Store(id, remoteConn)
	local.writeMessage(msgBytes)
	var ncResp *transport.NewConnectResponse
	t := time.After(time.Second * 20)
	select {
	case ncResp = <-remoteConn.connectInfoChan:
		if ncResp.Ojbk {
			logger.Info("new connect", zap.Uint64("id", id))
			return remoteConn
		} else {
			logger.Warn("not ojbk", zap.Uint64("id", id))
			return nil
		}
	case <-t:
		logger.Warn("wait ojbk timeout", zap.Uint64("id", id))
		return nil
	}
}

func (local *stickLocal) send(message *transport.Message) error {
	msgBytes, _ := proto.Marshal(message)
	logger.Debug("write msgBytes to ws", zap.Int("len", len(msgBytes)))
	return local.writeMessage(msgBytes)
}

func (local *stickLocal) get() (*transport.Message, error) {
	messageType, bytes, err := local.wsConn.ReadMessage()
	if messageType != websocket.BinaryMessage {
		logger.Warn("wrong message type", zap.Int("type", messageType))
		return nil, fmt.Errorf("")
	}
	if err != nil {
		logger.Error("read ws err", zap.Error(err))
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
			logger.Error("get transport.Message fail", zap.Error(err))
			break
		}
		connId := msg.Id
		logger.Debug("read msg", zap.Uint64("id", connId), zap.Any("msgType", msg.Type))
		//读出server发来的数据，找到对应的remoteConn，直接发过去
		if v, ok := local.connMap.Load(connId); ok {
			conn := v.(*remoteConn)
			logger.Debug("get remoteConn from connMap", zap.Uint64("id", connId))
			switch msg.Type {
			case transport.Message_Data:
				logger.Debug("writing msg.Data to bf", zap.Int("len", len(msg.Data)))
				_, err := conn.readBf.Write(msg.Data)
				if err != nil {
					logger.Error("write msg to bf fail", zap.Error(err))
					panic(err)
				}
			case transport.Message_NewConnectResponse:
				var ncResp transport.NewConnectResponse
				_ = proto.Unmarshal(msg.Data, &ncResp)
				logger.Info("newConnResp from server", zap.Uint64("id", msg.Id))
				conn.connectInfoChan <- &ncResp
			}
		} else {
			//TODO：服务器返回不存在的连接id
		}
	}
}

type remoteConn struct {
	stickLocal      *stickLocal
	id              uint64
	connectInfoChan chan *transport.NewConnectResponse
	readBf          *bytes.Buffer
}

func newRemoteConn(local *stickLocal, id uint64) *remoteConn {
	var buffer bytes.Buffer
	return &remoteConn{
		stickLocal:      local,
		id:              id,
		connectInfoChan: make(chan *transport.NewConnectResponse),
		readBf:          &buffer,
	}
}

func (rc *remoteConn) Write(p []byte) (int, error) {
	logger.Debug("write remoteConn", zap.Int("len", len(p)))
	message := &transport.Message{
		Id:   rc.id,
		Type: transport.Message_Data,
		Data: p,
	}
	rc.stickLocal.send(message)
	return len(p), nil
}

//TODO: 这么搞会cpu100%，感觉最简单的办法是循环加计数
func (rc *remoteConn) Read(p []byte) (int, error) {
	for rc.readBf.Len() == 0 {
		runtime.Gosched()
	}
	n, err := rc.readBf.Read(p)
	logger.Debug("read remoteConn from bf", zap.Int("len", n), zap.Error(err))
	return n, err
}
