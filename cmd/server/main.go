package main

import (
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"stick/model/transport"
	"stick/object"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/golang/protobuf/proto"

	"github.com/gorilla/websocket"
)

var addr = flag.String("addr", "0.0.0.0:8080", "http service address")

var (
	upgrader = websocket.Upgrader{}
	logger   = object.GetLogger()
)

func main() {
	flag.Parse()
	log.SetFlags(0)
	http.HandleFunc("/tt", echo)
	log.Fatal(http.ListenAndServe(*addr, nil))
}

type wsWrapper struct {
	*websocket.Conn
	*sync.Mutex
}

func echo(w http.ResponseWriter, r *http.Request) {
	token := r.Header.Get("token")
	if token != "abc" {
		logger.Warn("wrong token", zap.String("token", token))
		w.WriteHeader(http.StatusForbidden)
		return
	}
	logger.Info("get request", zap.String("remoteAddr", r.RemoteAddr))
	c, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		logger.Warn("upgrade fail", zap.Error(err))
		return
	}
	defer c.Close()
	wsWrapper := &wsWrapper{
		Conn:  c,
		Mutex: &sync.Mutex{},
	}
	var m sync.Map
	for {
		messageType, messageBytes, err := wsWrapper.ReadMessage()
		if err != nil {
			logger.Error("read ws error", zap.Error(err))
			break
		}
		if messageType != websocket.BinaryMessage {
			logger.Warn("error message type", zap.Int("type", messageType))
			continue
		}
		var message transport.Message
		err = proto.Unmarshal(messageBytes, &message)
		if err != nil {
			logger.Warn("message unmarshal fail", zap.Error(err))
			break
		}
		logger.Info("unmarshal msg", zap.Uint64("id", message.Id), zap.Any("type", message.Type))
		switch message.Type {
		case transport.Message_NewConnect:
			var newConnectData transport.NewConnect
			err = proto.Unmarshal(message.Data, &newConnectData)
			connID := message.Id
			conn := newConnect(connID, &newConnectData, wsWrapper)
			logger.Info("get newConnReq", zap.Uint64("id", connID), zap.Any("conn", conn))

			//建立连接，把连接加到map，返回ojbk
			m.Store(connID, conn)
			ojbk := &transport.NewConnectResponse{Ojbk: true}
			ojbkBytes, _ := proto.Marshal(ojbk)
			resp := &transport.Message{
				Id:   connID,
				Type: transport.Message_NewConnectResponse,
				Data: ojbkBytes,
			}
			respBytes, _ := proto.Marshal(resp)
			wsWrapper.Lock()
			err = wsWrapper.WriteMessage(websocket.BinaryMessage, respBytes)
			wsWrapper.Unlock()
			if err != nil {
				panic(err)
			}
			logger.Info("return ojbk", zap.Uint64("id", connID))
			f := func(w io.Writer, r io.Reader) {
				n, err := io.Copy(w, r)
				logger.Info("copy end", zap.Int64("len", n), zap.Error(err))
			}
			go f(conn, conn.remoteConn)
		case transport.Message_Data:
			var data = message.Data
			if v, ok := m.Load(message.Id); ok {
				conn := v.(*conn)
				logger.Info("write conn.bf", zap.Int("len", len(data)))
				index := 0
				for index != len(data) {
					n, err := conn.remoteConn.Write(data[index:])
					if err != nil {
						logger.Error("write msg to remoteConn fail", zap.Int("n", n), zap.Error(err))
						panic(err)
					}
					logger.Info("write msg to remoteConn", zap.Int("len", n))
					index += n
				}
			}
		}

	}
}

type conn struct {
	id         uint64
	remoteConn net.Conn
	wsWrapper  *wsWrapper
}

func newConnect(id uint64, connect *transport.NewConnect, wsWrapper *wsWrapper) *conn {
	var ip string
	//好像如果不用自定义dns的话，这里好像没啥用，dial可以直接输入的
	switch connect.AddrType {
	case transport.NewConnect_IPV4, transport.NewConnect_IPV6:
		ip = connect.Address
	case transport.NewConnect_Domain:
		ipList, err := net.LookupHost(connect.Address)
		if err != nil {
			panic(err)
		}
		ip = ipList[0]
	default:
		panic("bad addrtype")
	}
	remoteConn, err := net.Dial("tcp", fmt.Sprintf("%s:%d", ip, connect.Port))
	if err != nil {
		panic(err)
	}
	return &conn{
		id:         id,
		remoteConn: remoteConn,
		wsWrapper:  wsWrapper,
	}
}

func (c *conn) Write(p []byte) (int, error) {
	logger.Debug("write conn2local", zap.Int("len", len(p)))
	message := &transport.Message{
		Id:   c.id,
		Type: transport.Message_Data,
		Data: p,
	}
	msgBytes, _ := proto.Marshal(message)
	t := time.Now()
	c.wsWrapper.Lock()
	logger.Debug("lock wsWrapper", zap.Duration("lockUse", time.Since(t)))
	c.wsWrapper.WriteMessage(websocket.BinaryMessage, msgBytes)
	c.wsWrapper.Unlock()
	return len(p), nil
}
