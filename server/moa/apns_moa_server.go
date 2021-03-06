package moa

import (
	"github.com/blackbeans/go-moa/core"
	"github.com/blackbeans/go-moa/proxy"
	"github.com/go-errors/errors"
	"go-apns/apns"
	"go-apns/entry"
	"go-apns/server"
	"reflect"
	"regexp"
	"sync"
)

const (
	PUSH_TYPE_SIMPLE    = 0
	PUSH_TYPE_ENCHANCED = 1
)

var regx *regexp.Regexp

func init() {
	regx, _ = regexp.Compile("\\w+")
}

//apns发送的参数
type ApnsParams struct {
	ExpSeconds int                    `json:"expiredSeconds"`
	Token      string                 `json:"token"`
	Sound      string                 `json:"sound"`
	Badge      int                    `json:"badge"`
	Body       string                 `json:"body"`
	ExtArgs    map[string]interface{} `json:"extArgs"`
}

type IApnsService interface {
	SendNotification(pushType byte, params ApnsParams) (bool, error)
	QueryFeedback(limit int) ([]entry.Feedback, error)
}

type Bootstrap struct {
	service IApnsService
	app     *core.Application
}

func NewBootstrap(configPath string, option server.Option,
	feedbackChan chan *entry.Feedback,
	apnsClient *apns.ApnsClient) *Bootstrap {

	server := newApnsServer(&option, feedbackChan, apnsClient)
	app := core.NewApplcation(configPath, func() []proxy.Service {
		return []proxy.Service{
			proxy.Service{
				ServiceUri: "/service/bibi/apns-service",
				Interface:  (*IApnsService)(nil),
				Instance:   server}}
	})

	return &Bootstrap{service: server, app: app}
}

func (self *Bootstrap) Destroy() {
	self.app.DestroyApplication()
}

//-------------真正实现的
type ApnsServer struct {
	op           *server.Option
	feedbackChan chan *entry.Feedback //用于接收feedback的chan
	apnsClient   *apns.ApnsClient
	mutex        sync.Mutex
	expiredTime  uint32
}

func newApnsServer(option *server.Option,
	feedbackChan chan *entry.Feedback,
	apnsClient *apns.ApnsClient) ApnsServer {
	return ApnsServer{
		op:           option,
		feedbackChan: feedbackChan,
		apnsClient:   apnsClient,
		expiredTime:  option.ExpiredTime,
	}

}

func (self ApnsServer) SendNotification(pushType byte, params ApnsParams) (bool, error) {

	aps := entry.Aps{}
	if len(params.Sound) > 0 {
		aps.Sound = params.Sound
	}

	if params.Badge > 0 {
		aps.Badge = params.Badge
	}

	if len(params.Body) > 0 {
		aps.Alert = params.Body
	}

	//将空格的token拼装我一个
	tokenSplit := regx.FindAllString(params.Token, -1)
	var token string = ""
	for _, v := range tokenSplit {
		token += v
	}
	params.Token = token

	//拼接payload
	payload := entry.NewSimplePayLoadWithAps(aps)
	for k, v := range params.ExtArgs {
		//如果存在数据嵌套则返回错误，不允许数据多层嵌套
		if reflect.TypeOf(v).Kind() == reflect.Map {
			return false, errors.New("DEEP PAYLOAD BODY ITERATOR!")
		} else {
			payload.AddExtParam(k, v)
		}

	}

	//---------------发送逻辑
	var err error
	func() {
		defer func() {
			if er := recover(); nil != er {
				stack := er.(*errors.Error).ErrorStack()
				err = errors.New(stack)
			}
		}()

		//根据不同的类型发送不同的notification
		if PUSH_TYPE_SIMPLE == pushType {
			err = self.apnsClient.SendSimpleNotification(params.Token, *payload)
		} else if PUSH_TYPE_ENCHANCED == pushType {

			expiredTime := self.expiredTime
			if params.ExpSeconds > 0 {
				expiredTime = uint32(params.ExpSeconds)
			}
			err = self.apnsClient.SendEnhancedNotification(expiredTime,
				params.Token, *payload)
		}
	}()

	return nil == err, err
}
func (self ApnsServer) QueryFeedback(limit int) ([]entry.Feedback, error) {
	
		//本次获取多少个feedback
		if limit > 100 {
			return nil,errors.New("limit must be less than 100")
		} else {
			//发起了获取feedback的请求
			err := self.apnsClient.FetchFeedback(int(limit))
			if nil != err {
				return nil,err
			} else {
				//等待feedback数据
				packet := make([]entry.Feedback, 0, limit)
				var feedback *entry.Feedback
				for ; limit > 0; limit-- {
					feedback = <-self.feedbackChan
					if nil == feedback {
						break
					}
					packet = append(packet, *feedback)
				}
				return packet,nil
			}
		}
}
