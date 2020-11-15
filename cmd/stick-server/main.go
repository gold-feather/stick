package main

import (
	"flag"
	"log"
	"net/http"
	"stick/model/transport"

	"github.com/golang/protobuf/proto"

	"github.com/gorilla/websocket"
)

var addr = flag.String("addr", "localhost:8080", "http service address")

var upgrader = websocket.Upgrader{}

func echo(w http.ResponseWriter, r *http.Request) {
	token := r.Header.Get("token")
	if token != "abc" {
		w.WriteHeader(http.StatusForbidden)
		return
	}
	c, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Print("upgrade:", err)
		return
	}
	defer c.Close()
	for {
		messageType, messageBytes, err := c.ReadMessage()
		if err != nil {
			log.Println("read:", err)
			break
		}
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
		switch message.Type {
		case transport.Message_NewConnect:
			var newConnectData transport.NewConnect
			err = proto.Unmarshal(message.Data, &newConnectData)
			//TODO: 建立连接，把连接加到map，返回ojbk
			//TODO: 然后还得有个协程在读连接数据并转发给客户端

		case transport.Message_Data:
			var data = message.Data
			_ = data
			//TODO: 转发数据
		}

	}
}

func main() {
	flag.Parse()
	log.SetFlags(0)
	http.HandleFunc("/tt", echo)
	log.Fatal(http.ListenAndServe(*addr, nil))
}
