package mysql

import (
	"github.com/huanglishi/gofly-mqttv5/cluster/stat/colong"
	"github.com/huanglishi/gofly-mqttv5/corev5/messagev5"
)

type dbSender struct {
	curNodeName string
	c           *mysqlOrm
}

func NewMysqlClusterClient(curNodeName, mysqlUrl string, maxConn int, subMinNum, autoPeriod int) colong.NodeClientFace {
	db := newMysqlOrm(curNodeName, mysqlUrl, maxConn, subMinNum, autoPeriod)
	dbSend := &dbSender{
		curNodeName: curNodeName,
		c:           db,
	}
	colong.SetSender(dbSend)
	return dbSend
}

func (this *dbSender) Close() error {
	return nil
}

func (this *dbSender) SendOneNode(msg messagev5.Message,
	shareName, targetNode string,
	oneNodeSendSucFunc func(name string, message messagev5.Message),
	oneNodeSendFailFunc func(name string, message messagev5.Message)) {
	var e error
	switch msg := msg.(type) {
	case *messagev5.PublishMessage:
		if targetNode != "" && shareName != "" {
			e = this.c.SaveSharePub(targetNode, shareName, msg)
		} else {
			e = this.c.SavePub(msg)
		}
	case *messagev5.SubscribeMessage:
		e = this.c.SaveSub(msg)
	case *messagev5.UnsubscribeMessage:
		e = this.c.SaveUnSub(msg)
	}
	if e != nil {
		if oneNodeSendSucFunc != nil {
			go oneNodeSendSucFunc(targetNode, msg)
		}
	} else {
		if oneNodeSendFailFunc != nil {
			go oneNodeSendFailFunc(targetNode, msg)
		}
	}
}

func (this *dbSender) SendAllNode(msg messagev5.Message,
	allSuccess func(message messagev5.Message),
	oneNodeSendSucFunc func(name string, message messagev5.Message),
	oneNodeSendFailFunc func(name string, message messagev5.Message)) {
	var e error
	switch msg := msg.(type) {
	case *messagev5.PublishMessage:
		e = this.c.SavePub(msg)
	case *messagev5.SubscribeMessage:
		e = this.c.SaveSub(msg)
	case *messagev5.UnsubscribeMessage:
		e = this.c.SaveUnSub(msg)
	}
	if e != nil {
		if allSuccess != nil {
			go allSuccess(msg)
		}
		if oneNodeSendSucFunc != nil {
			go oneNodeSendSucFunc(colong.AllNodeName, msg)
		}
	} else {
		if oneNodeSendFailFunc != nil {
			go oneNodeSendFailFunc(colong.AllNodeName, msg)
		}
	}
}
