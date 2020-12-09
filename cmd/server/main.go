package main

import (
	"bytes"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"runtime"
	"stick/model/transport"
	"stick/object"
	"sync"

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
	var m sync.Map
	for {
		messageType, messageBytes, err := c.ReadMessage()
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
			conn := newConnect(connID, &newConnectData, c)
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
			err = c.WriteMessage(websocket.BinaryMessage, respBytes)
			if err != nil {
				panic(err)
			}
			logger.Info("return ojbk", zap.Uint64("id", connID))
			//TODO: 然后还得有个协程在读连接数据并转发给客户端
			f := func(w io.Writer, r io.Reader) {
				n, err := io.Copy(w, r)
				logger.Info("copy end", zap.Int64("len", n), zap.Error(err))
			}
			go f(conn, conn.remoteConn)
			go f(conn.remoteConn, conn)
		case transport.Message_Data:
			var data = message.Data
			if v, ok := m.Load(message.Id); ok {
				conn := v.(*conn)
				logger.Info("write conn.bf", zap.Int("len", len(data)))
				conn.readBf.Write(data)
			}
		}

	}
}

type conn struct {
	id         uint64
	remoteConn net.Conn
	wb         *websocket.Conn
	readBf     *bytes.Buffer
}

func newConnect(id uint64, connect *transport.NewConnect, wb *websocket.Conn) *conn {
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
	var buffer bytes.Buffer
	return &conn{
		id:         id,
		remoteConn: remoteConn,
		wb:         wb,
		readBf:     &buffer,
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
	c.wb.WriteMessage(websocket.BinaryMessage, msgBytes)
	return len(p), nil
}

func (c *conn) Read(p []byte) (int, error) {
	for c.readBf.Len() == 0 {
		runtime.Gosched()
	}
	n, err := c.readBf.Read(p)
	logger.Debug("read conn2local from bf", zap.Int("len", n), zap.Error(err))
	return n, err
}
