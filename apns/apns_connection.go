package apns

import (
	"crypto/tls"
	"errors"
	log "github.com/blackbeans/log4go"
	"go-apns/entry"
	"net"
	"reflect"
	"time"
)

type IConn interface {
	Open() error
	IsAlive() bool
	Close()
	sendMessage(msg *entry.Message) error
}

const (
	CONN_READ_BUFFER_SIZE  = 256
	CONN_WRITE_BUFFER_SIZE = 512
)

type ApnsConnection struct {
	cert         tls.Certificate //ssl证书
	hostport     string
	deadline     time.Duration
	conn         *tls.Conn
	responseChan chan<- *entry.Response
	alive        bool  //是否存活
	connectionId int32 //当前连接的标识
}

func NewApnsConnection(responseChan chan<- *entry.Response,
	certificates tls.Certificate, hostport string, deadline time.Duration, connectionId int32) (error, *ApnsConnection) {

	conn := &ApnsConnection{
		cert:         certificates,
		hostport:     hostport,
		deadline:     deadline,
		responseChan: responseChan,
		connectionId: connectionId}
	return conn.Open(), conn
}

func (self *ApnsConnection) Open() error {
	ch := make(chan error, 1)
	go func() {
		ch <- self.dial()
	}()

	//创建打开连接60s超时
	select {
	case err := <-ch:
		if nil != err {
			return err
		}
		self.alive = true
		//启动读取数据
		go self.waitRepsonse()

	case <-time.After(60 * time.Second):
		return errors.New("open apnsconnection timeout!")
	}
	return nil
}

func (self *ApnsConnection) waitRepsonse() {
	//这里需要优化是否同步读取结果
	buff := make([]byte, entry.ERROR_RESPONSE, entry.ERROR_RESPONSE)
	//同步读取当前conn的结果
	length, err := self.conn.Read(buff[:entry.ERROR_RESPONSE])
	if nil != err || length != len(buff) {
		log.InfoLog("push_client", "CONNECTION|%s|READ RESPONSE|FAIL|%s|%d", self.conn.RemoteAddr().String(), err, buff)
	} else {
		response := &entry.Response{}
		response.Unmarshal(self.connectionId, buff)
		self.responseChan <- response
	}

	//已经读取到了错误信息直接关闭
	self.Close()

}

func (self *ApnsConnection) name() string {
	return reflect.TypeOf(*self).Name()
}

func (self *ApnsConnection) dial() error {

	config := tls.Config{}
	config.Certificates = []tls.Certificate{self.cert}
	config.InsecureSkipVerify = true
	//自定义dialer
	dialer := new(net.Dialer)
	dialer.KeepAlive = 10 * time.Minute
	dialer.Timeout = 30 * time.Second

	conn, err := tls.DialWithDialer(dialer, "tcp", self.hostport, &config)
	if nil != err {
		//connect fail
		log.WarnLog("push_client", "CONNECTION|%s|DIAL CONNECT|FAIL|%s|%s", self.name(), self.hostport, err.Error())
		return err
	}

	for {
		state := conn.ConnectionState()
		if state.HandshakeComplete {
			log.InfoLog("push_client", "CONNECTION|%s|HANDSHAKE SUCC", self.name())
			break
		}
		time.Sleep(1 * time.Second)
	}
	self.conn = conn
	return nil
}

func (self *ApnsConnection) sendMessage(msg *entry.Message) error {
	if !self.alive {
		//存活但是不适合握手完成状态则失败
		return errors.New("CONNECTION|SEND MESSAGE|FAIL|Connection Closed!")
	}

	err, packet := msg.Encode()
	if nil != err {
		return err
	}
	//消息使用当前连接发送做记录
	msg.ConnectionId = self.connectionId
	length, sendErr := self.conn.Write(packet)
	if nil != sendErr || length != len(packet) {
		log.WarnLog("push_client", "CONNECTION|SEND MESSAGE|FAIL|%s", sendErr)
	} else {
		log.DebugLog("push_client", "CONNECTION|SEND MESSAGE|SUCC")

	}
	return sendErr

}

func (self *ApnsConnection) IsAlive() bool {
	return self.alive
}

func (self *ApnsConnection) Close() {

	self.alive = false
	self.conn.Close()
	log.InfoLog("push_client", "APNS CONNECTION|%s|CLOSED ...", self.name())
}
