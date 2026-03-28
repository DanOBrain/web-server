package client

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"sync"
	"time"
	"web-server/src/tracer"

	"github.com/gorilla/websocket"
)

const (
	socketBufferSize  = 1024
	messageBufferSize = 256
)

// Структура сообщения
type Message struct {
	Type    string `json:"type"`
	Content string `json:"content,omitempty"`
	Sender  string `json:"sender,omitempty"`
	Token   string `json:"token,omitempty"`
	Name    string `json:"name,omitempty"`
}

// Структура клиента
type Client struct {
	socket *websocket.Conn
	send   chan []byte
	room   *Room
	name   string
	token  string
	mu     sync.RWMutex
}

var upgrader = &websocket.Upgrader{
	WriteBufferSize: socketBufferSize,
	ReadBufferSize:  socketBufferSize,
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
}

func (c *Client) Read() {
	for {
		_, msg, err := c.socket.ReadMessage()
		if err != nil {
			break
		}

		var message Message
		if err := json.Unmarshal(msg, &message); err != nil {
			c.room.Tracer.Trace("Ошибка чтения сообщения:", err)
			continue
		}

		switch message.Type {
		case "auth":
			// авторизация пользователя
			c.mu.Lock()
			c.name = message.Name
			c.token = message.Token
			c.mu.Unlock()

			// Отправка приветствия при вступлении
			welcome := Message{
				Type:    "system",
				Content: fmt.Sprintf("Приветствую %s в чате!", message.Name),
				Sender:  "system",
			}
			if welcomeMsg, err := json.Marshal(welcome); err == nil {
				c.send <- welcomeMsg
			}

			// Сообщение о вступлении
			joinMsg := Message{
				Type:    "system",
				Content: fmt.Sprintf("%s присоединился к чату", message.Name),
				Sender:  "system",
			}
			if joinMsgBytes, err := json.Marshal(joinMsg); err == nil {
				c.room.Forward <- joinMsgBytes
			}

		case "message":
			c.mu.RLock()
			senderName := c.name
			c.mu.RUnlock()

			if senderName != "" {
				chatMsg := Message{
					Type:    "message",
					Content: message.Content,
					Sender:  senderName,
				}
				if msgBytes, err := json.Marshal(chatMsg); err == nil {
					c.room.Forward <- msgBytes
				}
			}
		case "leave":
			c.mu.RLock()
			clientName := c.name
			c.mu.RUnlock()

			if clientName != "" {
				c.room.Tracer.Trace(clientName, " - запрос на выход")
			}
			c.socket.Close()
			return
		}
	}
	c.socket.Close()
}

func (c *Client) Write() {
	for msg := range c.send {
		if err := c.socket.WriteMessage(websocket.TextMessage, msg); err != nil {
			break
		}
	}
	c.socket.Close()
}

// Структура комнаты
type Room struct {
	Forward chan []byte
	Join    chan *Client
	Leave   chan *Client
	Clients map[*Client]bool
	Tracer  tracer.Tracer
	tokens  map[string]string
	mu      sync.RWMutex
}

func NewRoom() *Room {
	return &Room{
		Forward: make(chan []byte),
		Join:    make(chan *Client),
		Leave:   make(chan *Client),
		Clients: make(map[*Client]bool),
		tokens:  make(map[string]string),
	}
}

func (r *Room) Run() {
	for {
		select {
		case client := <-r.Join:
			r.mu.Lock()
			r.Clients[client] = true
			r.mu.Unlock()
			r.Tracer.Trace("Новый клиент присоединился")

		case client := <-r.Leave:
			// Get client name before removing
			client.mu.RLock()
			clientName := client.name
			client.mu.RUnlock()

			r.mu.Lock()
			delete(r.Clients, client)
			r.mu.Unlock()

			if clientName != "" {
				leaveMsg := Message{
					Type:    "system",
					Content: fmt.Sprintf("%s покинул чат", clientName),
					Sender:  "system",
				}
				if msgBytes, err := json.Marshal(leaveMsg); err == nil {
					r.mu.RLock()
					for c := range r.Clients {
						select {
						case c.send <- msgBytes:
							r.Tracer.Trace(" -- отправил запрос о выходе", c.name)
						default:
							r.Tracer.Trace(" -- ошибка отправки запроса о выходе")
						}
					}
					r.mu.RUnlock()
				}
				r.Tracer.Trace(clientName, " покинул чат")
			} else {
				r.Tracer.Trace("Клиент вышел")
			}

			close(client.send)

		case msg := <-r.Forward:
			r.mu.RLock()
			for client := range r.Clients {
				select {
				case client.send <- msg:
					r.Tracer.Trace(" -- отправлено клиенту")
				default:
					r.Tracer.Trace(" -- ошибка отправки")
				}
			}
			r.mu.RUnlock()
		}
	}
}

// GenerateToken создает токен для идентификации пользователя
func GenerateToken(name string) string {
	return fmt.Sprintf("token_%s_%d", name, time.Now().UnixNano())
}

func (r *Room) ServeHTTP(writer http.ResponseWriter, req *http.Request) {
	socket, err := upgrader.Upgrade(writer, req, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ServeHTTP: %v", err)
		return
	}

	client := &Client{
		socket: socket,
		send:   make(chan []byte, messageBufferSize),
		room:   r,
	}

	r.Join <- client
	defer func() {
		r.Leave <- client
	}()

	go client.Write()
	client.Read()
}
