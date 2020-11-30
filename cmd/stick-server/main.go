package main

import (
	"bytes"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"stick/model/transport"
	"sync"

	"github.com/golang/protobuf/proto"

	"github.com/gorilla/websocket"
)

var addr = flag.String("addr", "localhost:8080", "http service address")

var upgrader = websocket.Upgrader{}

func main() {
	flag.Parse()
	log.SetFlags(0)
	http.HandleFunc("/tt", echo)
	log.Fatal(http.ListenAndServe(*addr, nil))
}

func echo(w http.ResponseWriter, r *http.Request) {
	token := r.Header.Get("token")
	if token != "abc" {
		log.Println(token)
		w.WriteHeader(http.StatusForbidden)
		return
	}
	log.Printf("get request from %s", r.RemoteAddr)
	c, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("upgrade fail: %v\n", err)
		return
	}
	defer c.Close()
	var m sync.Map
	for {
		messageType, messageBytes, err := c.ReadMessage()
		if err != nil {
			log.Println("read:", err)
			break
		}
		log.Printf("read a WSmessage, type:%d\n", messageType)
		if messageType != websocket.BinaryMessage {
			log.Printf("msgType: %d", messageType)
			continue
		}
		var message transport.Message
		err = proto.Unmarshal(messageBytes, &message)
		if err != nil {
			log.Println(err)
			break
		}
		log.Printf("unmarshal msg, type: %d, id: %d\n", message.Type, message.Id)
		switch message.Type {
		case transport.Message_NewConnect:
			var newConnectData transport.NewConnect
			err = proto.Unmarshal(message.Data, &newConnectData)
			connID := message.Id
			conn := newConnect(connID, &newConnectData, c)
			log.Printf("get newConnReq, id:%d, conn: %v\n", connID, conn)

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
			log.Printf("return ojbk for id:%d\n", connID)
			//TODO: 然后还得有个协程在读连接数据并转发给客户端
			go io.Copy(conn, conn.remoteConn)
			go io.Copy(conn.remoteConn, conn)
		case transport.Message_Data:
			var data = message.Data
			if v, ok := m.Load(message.Id); ok {
				conn := v.(*conn)
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
	return c.readBf.Read(p)
}
