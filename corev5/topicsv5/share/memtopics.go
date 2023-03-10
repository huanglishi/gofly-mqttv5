// 共享订阅
package share

import (
	"fmt"
	"math/rand"
	"reflect"
	"sync"
	"time"

	"github.com/huanglishi/gofly-mqttv5/corev5/messagev5"
	"github.com/huanglishi/gofly-mqttv5/corev5/topicsv5"
	"github.com/huanglishi/gofly-mqttv5/logger"
)

var (
	// MaxQosAllowed is the maximum QOS supported by this server
	MaxQosAllowed = messagev5.QosExactlyOnce
)

var _ ShareTopicsProvider = (*memTopics)(nil)

type memTopics struct {
	// Sub/unsub mutex
	smu sync.RWMutex
	// Subscription tree
	sroot *snode

	// Retained messagev5 mutex
	rmu sync.RWMutex
	// Retained messages topic tree
	rroot *rnode
}

func memTopicInit() {
	Register("", NewMemProvider())
	logger.Logger.Info("开启mem进行share topic管理")
}

// NewMemProvider returns an new instance of the memTopics, which is implements the
// TopicsProvider interface. memProvider is a hidden struct that stores the topic
// subscriptions and retained messages in memory. The content is not persistend so
// when the server goes, everything will be gone. Use with care.
// NewMemProvider返回memTopics的一个新实例，该实例实现了
// TopicsProvider接口。memProvider是存储主题的隐藏结构
// 订阅并保留内存中的消息。内容不是这样持久化的
// 当服务器关闭时，所有东西都将消失。小心使用。
func NewMemProvider() *memTopics {
	return &memTopics{
		sroot: newSNode(),
		rroot: newRNode(),
	}
}

// 订阅主题
func (this *memTopics) Subscribe(shareName []byte, subs topicsv5.Sub, sub interface{}) (byte, error) {
	if !messagev5.ValidQos(subs.Qos) {
		return messagev5.QosFailure, fmt.Errorf("Invalid QoS %d", subs.Qos)
	}

	if sub == nil {
		return messagev5.QosFailure, fmt.Errorf("Subscriber cannot be nil")
	}

	this.smu.Lock()
	defer this.smu.Unlock()

	if subs.Qos > MaxQosAllowed {
		subs.Qos = MaxQosAllowed
	}
	if err := this.sroot.sinsert(shareName, subs, sub); err != nil {
		return messagev5.QosFailure, err
	}

	return subs.Qos, nil
}

func (this *memTopics) Unsubscribe(topic, shareName []byte, sub interface{}) error {
	this.smu.Lock()
	defer this.smu.Unlock()

	return this.sroot.sremove(topic, shareName, sub)
}

// Returned values will be invalidated by the next Subscribers call
// 返回的值将在下一次订阅者调用时失效
func (this *memTopics) Subscribers(topic, shareName []byte, qos byte, subs *[]interface{}, qoss *[]topicsv5.Sub) error {
	if !messagev5.ValidQos(qos) {
		return fmt.Errorf("Invalid QoS %d", qos)
	}

	this.smu.RLock()
	defer this.smu.RUnlock()

	*subs = (*subs)[0:0]
	*qoss = (*qoss)[0:0]

	return this.sroot.smatch(topic, shareName, qos, subs, qoss)
}

// 获取所有的共享订阅，k: 主题，v: 该主题的所有共享组
// FIXME 这个会在服务停止时调用，但是再某些情况会调用失败，比如与redis也断开了连接
func (this *memTopics) AllSubInfo() (map[string][]string, error) {
	v := make(map[string][]string)
	this.smu.RLock()
	defer this.smu.RUnlock()
	err := this.sroot.smatchAll(v)
	return v, err
}

func (this *memTopics) Retain(msg *messagev5.PublishMessage, shareName []byte) error {
	this.rmu.Lock()
	defer this.rmu.Unlock()

	// So apparently, at least according to the MQTT Conformance/Interoperability
	// Testing, that a payload of 0 means delete the retain messagev5.
	//很明显，至少根据MQTT一致性/互操作性
	//测试，有效载荷为0表示删除retain消息。
	// https://eclipse.org/paho/clients/testing/
	if len(msg.Payload()) == 0 {
		return this.rroot.rremove(msg.Topic(), shareName)
	}

	return this.rroot.rinsert(msg.Topic(), shareName, msg)
}

func (this *memTopics) Retained(topic, shareName []byte, msgs *[]*messagev5.PublishMessage) error {
	this.rmu.RLock()
	defer this.rmu.RUnlock()

	return this.rroot.rmatch(topic, shareName, msgs)
}

func (this *memTopics) Close() error {
	this.sroot = nil
	this.rroot = nil
	return nil
}

type sins struct {
	subs []interface{}
	qos  []byte
}

// subscrition nodes
// subscrition节点
type snode struct {
	// If this is the end of the topic string, then add subscribers here
	//如果这是主题字符串的结尾，那么在这里添加订阅者
	shares map[string]*sins // shareName : *sin
	// Otherwise add the next topic level here
	snodes map[string]*snode // topic: nextNode
}

func newSNode() *snode {
	return &snode{
		shares: make(map[string]*sins),
		snodes: make(map[string]*snode),
	}
}

func (this *snode) sinsert(shareName []byte, subs topicsv5.Sub, sub interface{}) error {
	// If there's no more topic levels, that means we are at the matching snode
	// to insert the subscriber. So let's see if there's such subscriber,
	// if so, update it. Otherwise insert it.
	//如果没有更多的主题级别，这意味着我们在匹配的snode
	//插入订阅。让我们看看是否有这样的订阅者，
	//如果有，更新它。否则插入它。
	if len(subs.Topic) == 0 {
		sn := string(shareName)
		if v, ok := this.shares[sn]; ok {
			//让我们看看用户是否已经在名单上了。如果是的,更新
			// QoS，然后返回。
			for i := range v.subs { // 重复订阅，可更新qos
				if equal(v.subs[i], sub) {
					v.qos[i] = subs.Qos
					return nil
				}
			}
			v.subs = append(v.subs, sub)
			v.qos = append(v.qos, subs.Qos)
			return nil
		}
		// Otherwise add.
		sin := &sins{
			subs: make([]interface{}, 0),
			qos:  make([]byte, 0),
		}
		sin.subs = append(sin.subs, sub)
		sin.qos = append(sin.qos, subs.Qos)
		this.shares[sn] = sin

		return nil
	}

	// Not the last level, so let's find or create the next level snode, and
	// recursively call it's insert().
	//不是最后一层，让我们查找或创建下一层snode，和
	//递归调用它的insert()。

	// ntl = next topic level
	// ntl =下一个主题级别
	ntl, rem, err := nextTopicLevel(subs.Topic)
	if err != nil {
		return err
	}

	level := string(ntl)

	// Add snode if it doesn't already exist
	n, ok := this.snodes[level]
	if !ok {
		n = newSNode()
		this.snodes[level] = n
	}
	subs.Topic = rem
	return n.sinsert(shareName, subs, sub)
}

// This remove implementation ignores the QoS, as long as the subscriber
// matches then it's removed
func (this *snode) sremove(topic, shareName []byte, sub interface{}) error {
	// If the topic is empty, it means we are at the final matching snode. If so,
	// let's find the matching subscribers and remove them.
	//如果主题是空的，这意味着我们到达了最后一个匹配的snode。 如果是这样,
	//让我们找到匹配的订阅服务器并删除它们。
	if len(topic) == 0 {
		//如果订阅者== nil，则发出删除当前shareName的所有订阅者的信号
		// 如果shareName为空，则标识删除当前主题下的所有共享主题订阅者们
		if sub == nil {
			if len(shareName) == 0 {
				this.shares = make(map[string]*sins)
			} else {
				sn := string(shareName)
				// 共享订阅如果没有订阅者了，不需要再维护这个共享主题了
				delete(this.shares, sn)
				//if v, ok := this.shares[sn];ok{
				//	v.subs = v.subs[0:0]
				//	v.qos = v.qos[0:0]
				//}
			}
			return nil
		}
		sn := string(shareName)
		if v, ok := this.shares[sn]; ok {
			//如果我们找到了用户，就把它从列表中删除
			for i := range v.subs {
				if equal(v.subs[i], sub) {
					v.subs = append(v.subs[:i], v.subs[i+1:]...)
					v.qos = append(v.qos[:i], v.qos[i+1:]...)
					return nil
				}
			}
		}
		return fmt.Errorf("memtopics/remove: No topic found for subscriber")
	}

	// Not the last level, so let's find the next level snode, and recursively
	// call it's remove().
	// ntl = next topic level
	//不是最后一层，所以让我们递归地找到下一层snode
	//调用它的remove()。
	// ntl =下一个主题级别
	ntl, rem, err := nextTopicLevel(topic)
	if err != nil {
		return err
	}

	level := string(ntl)

	// Find the snode that matches the topic level
	n, ok := this.snodes[level]
	if !ok {
		return fmt.Errorf("memtopics/remove: No topic found")
	}

	// Remove the subscriber from the next level snode
	if err := n.sremove(rem, shareName, sub); err != nil {
		return err
	}

	// If there are no more subscribers and snodes to the next level we just visited
	// let's remove it
	var tag = true
	for _, v := range n.shares {
		if len(v.subs) != 0 {
			tag = false
			break
		}
	}
	if tag && len(n.snodes) == 0 {
		delete(this.snodes, level)
	}

	return nil
}

// smatch() returns all the subscribers that are subscribed to the topic. Given a topic
// with no wildcards (publish topic), it returns a list of subscribers that subscribes
// to the topic. For each of the level names, it's a match
// - if there are subscribers to '#', then all the subscribers are added to result set
// smatch()返回订阅该主题的所有订阅方。给定一个主题
//没有通配符(发布主题)，它返回订阅的订阅方列表
//回到主题。对于每个级别名称，它都是匹配的
// -如果“#”中有订阅者，那么所有的订阅者都将被添加到结果集中

//非规范评注
//例如, 如果客户端订阅主题 “sport/tennis/player1/#”, 它会收到使用下列主题名发布的消息:
//• “sport/tennis/player1”
//• “sport/tennis/player1/ranking
//• “sport/tennis/player1/score/wimbledon”
//
//非规范评注
//• “sport/#”也匹配单独的“sport”主题名, 因为#包括它的父级.
//• “#”是有效的, 会收到所有的应用消息.
//• “sport/tennis/#”也是有效的.
//• “sport/tennis#”是无效的.
//• “sport/tennis/#/ranking”是无效的.

//非规范评注
//例如, “sport/tennis/+”匹配“sport/tennis/player1”和“sport/tennis/player2”,
//但是不匹配“sport/tennis/player1/ranking”.同时, 由于单层通配符只能匹配一个层级,
//“sport/+”不匹配“sport”但是却匹配“sport/”.
//• “+”是有效的.
//• “+/tennis/#”是有效的.
//• “sport+”是无效的.
//• “sport/+/player1”是有效的.
//• “/finance”匹配“+/+”和“/+”, 但是不匹配“+”.

func (this *snode) smatch(topic, shareName []byte, qos byte, subs *[]interface{}, qoss *[]topicsv5.Sub) error {
	// If the topic is empty, it means we are at the final matching snode. If so,
	// let's find the subscribers that match the qos and append them to the list.
	//如果主题是空的，这意味着我们到达了最后一个匹配的snode。如果是这样,
	//让我们找到与qos匹配的订阅服务器并将它们附加到列表中。
	if len(topic) == 0 {
		this.matchQos(qos, subs, qoss, shareName)
		if v, ok := this.snodes["#"]; ok {
			v.matchQos(qos, subs, qoss, shareName)
		}
		if v, ok := this.snodes["+"]; ok {
			v.matchQos(qos, subs, qoss, shareName)
		}
		return nil
	}

	// ntl = next topic level
	//rem和err都等于nil，意味着是 sss/sss这种后面没有/结尾的
	//len(rem)==0和err等于nil，意味着是 sss/sss/这种后面有/结尾的
	// rem用来做 #和+匹配时有用
	ntl, rem, err := nextTopicLevel(topic)
	if err != nil {
		return err
	}

	level := string(ntl)
	for k, n := range this.snodes {
		// If the key is "#", then these subscribers are added to the result set
		if k == MWC {
			n.matchQos(qos, subs, qoss, shareName)
		} else if k == SWC || k == level {
			if rem != nil {
				if err := n.smatch(rem, shareName, qos, subs, qoss); err != nil {
					return err
				}
			} else { // 这个不需要匹配最后还有+的订阅者
				n.matchQos(qos, subs, qoss, shareName)
				if v, ok := n.snodes["#"]; ok {
					v.matchQos(qos, subs, qoss, shareName)
				}
			}
		}
	}

	return nil
}

// 匹配所有的
// DPS or BPS
func (this *snode) smatchAll(v map[string][]string) error {
	for k, n := range this.snodes {
		n.matchAll(v, []byte(k))
	}
	return nil
}

// retained messagev5 nodes
// 保留信息节点
type rnode struct {
	// If this is the end of the topic string, then add retained messages here
	//如果这是主题字符串的结尾，那么在这里添加保留的消息
	msg *messagev5.PublishMessage
	buf []byte

	// Otherwise add the next topic level here
	rnodes map[string]*rnode
}

func newRNode() *rnode {
	return &rnode{
		rnodes: make(map[string]*rnode),
	}
}

func (this *rnode) rinsert(topic, shareName []byte, msg *messagev5.PublishMessage) error {
	// If there's no more topic levels, that means we are at the matching rnode.
	if len(topic) == 0 {
		l := msg.Len()

		// Let's reuse the buffer if there's enough space
		if l > cap(this.buf) {
			this.buf = make([]byte, l)
		} else {
			this.buf = this.buf[0:l]
		}

		if _, err := msg.Encode(this.buf); err != nil {
			return err
		}

		// Reuse the messagev5 if possible
		if this.msg == nil {
			this.msg = messagev5.NewPublishMessage()
		}

		if _, err := this.msg.Decode(this.buf); err != nil {
			return err
		}

		return nil
	}

	// Not the last level, so let's find or create the next level snode, and
	// recursively call it's insert().

	// ntl = next topic level
	ntl, rem, err := nextTopicLevel(topic)
	if err != nil {
		return err
	}

	level := string(ntl)

	// Add snode if it doesn't already exist
	n, ok := this.rnodes[level]
	if !ok {
		n = newRNode()
		this.rnodes[level] = n
	}

	return n.rinsert(rem, shareName, msg)
}

// Remove the retained messagev5 for the supplied topic
func (this *rnode) rremove(topic, shareName []byte) error {
	// If the topic is empty, it means we are at the final matching rnode. If so,
	// let's remove the buffer and messagev5.
	if len(topic) == 0 {
		this.buf = nil
		this.msg = nil
		return nil
	}

	// Not the last level, so let's find the next level rnode, and recursively
	// call it's remove().

	// ntl = next topic level
	ntl, rem, err := nextTopicLevel(topic)
	if err != nil {
		return err
	}

	level := string(ntl)

	// Find the rnode that matches the topic level
	n, ok := this.rnodes[level]
	if !ok {
		return fmt.Errorf("memtopics/rremove: No topic found")
	}

	// Remove the subscriber from the next level rnode
	if err := n.rremove(rem, shareName); err != nil {
		return err
	}

	// If there are no more rnodes to the next level we just visited let's remove it
	if len(n.rnodes) == 0 {
		delete(this.rnodes, level)
	}

	return nil
}

// rmatch() finds the retained messages for the topic and qos provided. It's somewhat
// of a reverse match compare to match() since the supplied topic can contain
// wildcards, whereas the retained messagev5 topic is a full (no wildcard) topic.
func (this *rnode) rmatch(topic, shareName []byte, msgs *[]*messagev5.PublishMessage) error {
	// If the topic is empty, it means we are at the final matching rnode. If so,
	// add the retained msg to the list.
	if len(topic) == 0 {
		if this.msg != nil {
			*msgs = append(*msgs, this.msg)
		}
		return nil
	}

	// ntl = next topic level
	ntl, rem, err := nextTopicLevel(topic)
	if err != nil {
		return err
	}

	level := string(ntl)

	if level == MWC {
		// If '#', add all retained messages starting this node
		this.allRetained(msgs)
	} else if level == SWC {
		// If '+', check all nodes at this level. Next levels must be matched.
		for _, n := range this.rnodes {
			if err := n.rmatch(rem, shareName, msgs); err != nil {
				return err
			}
		}
	} else {
		// Otherwise, find the matching node, go to the next level
		if n, ok := this.rnodes[level]; ok {
			if err := n.rmatch(rem, shareName, msgs); err != nil {
				return err
			}
		}
	}

	return nil
}

func (this *rnode) allRetained(msgs *[]*messagev5.PublishMessage) {
	if this.msg != nil {
		*msgs = append(*msgs, this.msg)
	}

	for _, n := range this.rnodes {
		n.allRetained(msgs)
	}
}

const (
	stateCHR byte = iota // Regular character 普通字符
	stateMWC             // Multi-level wildcard 多层次的通配符
	stateSWC             // Single-level wildcard 单层通配符
	stateSEP             // Topic level separator 主题水平分隔符
	stateSYS             // System level topic ($) 系统级主题($)
)

// Returns topic level, remaining topic levels and any errors
// 返回主题级别、剩余的主题级别和任何错误
func nextTopicLevel(topic []byte) ([]byte, []byte, error) {
	s := stateCHR

	//遍历topic，判断是何种类型的主题
	for i, c := range topic {
		switch c {
		case '/':
			if s == stateMWC {
				//多层次通配符发现的主题，它不是在最后一层
				return nil, nil, fmt.Errorf("memtopics/nextTopicLevel: Multi-level wildcard found in topic and it's not at the last level")
			}

			if i == 0 {
				return []byte(SWC), topic[i+1:], nil
			}

			return topic[:i], topic[i+1:], nil

		case '#':
			if i != 0 {
				//通配符“#”必须占据整个主题级别
				return nil, nil, fmt.Errorf("memtopics/nextTopicLevel: Wildcard character '#' must occupy entire topic level")
			}

			s = stateMWC

		case '+':
			if i != 0 {
				//通配符“+”必须占据整个主题级别
				return nil, nil, fmt.Errorf("memtopics/nextTopicLevel: Wildcard character '+' must occupy entire topic level")
			}

			s = stateSWC

		case '$':
			if i == 0 {
				//不能发布到$ topics
				return nil, nil, fmt.Errorf("memtopics/nextTopicLevel: Cannot publish to $ topics")
			}

			s = stateSYS

		default:
			if s == stateMWC || s == stateSWC {
				//通配符“#”和“+”必须占据整个主题级别
				return nil, nil, fmt.Errorf("memtopics/nextTopicLevel: Wildcard characters '#' and '+' must occupy entire topic level")
			}

			s = stateCHR
		}
	}

	// If we got here that means we didn't hit the separator along the way, so the
	// topic is either empty, or does not contain a separator. Either way, we return
	// the full topic
	//如果我们到了这里，那就意味着我们没有中途到达分隔符，所以
	//主题要么为空，要么不包含分隔符。不管怎样，我们都会回来
	//完整的主题
	return topic, nil, nil
}

// The QoS of the payload messages sent in response to a subscription must be the
// minimum of the QoS of the originally published messagev5 (in this case, it's the
// qos parameter) and the maximum QoS granted by the server (in this case, it's
// the QoS in the topic tree).
//
// It's also possible that even if the topic matches, the subscriber is not included
// due to the QoS granted is lower than the published messagev5 QoS. For example,
// if the client is granted only QoS 0, and the publish messagev5 is QoS 1, then this
// client is not to be send the published messagev5.
// 响应订阅发送的有效负载消息的QoS必须为
// 原始发布消息的QoS的最小值(在本例中为
// qos参数)和服务器授予的最大qos(在本例中为
// 主题树中的QoS)。
// 也有可能，即使主题匹配，订阅服务器也不包括在内
// 由于授予的QoS低于发布消息的QoS。例如,
// 如果只授予客户端QoS 0，且发布消息为QoS 1，则为
// 客户端不能发送已发布的消息。
func (this *snode) matchQos(qos byte, subs *[]interface{}, qoss *[]topicsv5.Sub, shareName []byte) {
	if len(shareName) == 0 {
		for _, sub := range this.shares {
			if len(sub.subs) == 0 {
				continue
			}
			// 采用随机匹配
			i := rand.New(rand.NewSource(time.Now().UnixNano())).Intn(len(sub.subs))
			// TODO 修改为取二者最小值qos
			if qos <= sub.qos[i] {
				*qoss = append(*qoss, topicsv5.Sub{Qos: qos})
			} else {
				*qoss = append(*qoss, topicsv5.Sub{Qos: sub.qos[i]})
			}
			*subs = append(*subs, sub.subs[i])
		}
		return
	}
	if sub, ok := this.shares[string(shareName)]; ok {
		if len(sub.subs) == 0 {
			return
		}
		// 采用随机匹配
		i := rand.New(rand.NewSource(time.Now().UnixNano())).Intn(len(sub.subs))
		// TODO 修改为取二者最小值qos
		if qos <= sub.qos[i] {
			*qoss = append(*qoss, topicsv5.Sub{Qos: qos})
		} else {
			*qoss = append(*qoss, topicsv5.Sub{Qos: sub.qos[i]})
		}
		*subs = append(*subs, sub.subs[i])
	}
}

// 获取所有的
func (this *snode) matchAll(v map[string][]string, topic []byte) {
	tp := string(topic)
	for i, _ := range this.shares {
		if _, ok := v[tp]; !ok {
			v[tp] = make([]string, 0)
		}
		v[tp] = append(v[tp], i)
	}
	for p, sn := range this.snodes {
		temp := topic
		temp = append(temp, '/')
		temp = append(temp, p...)
		sn.matchAll(v, temp)
	}
}
func equal(k1, k2 interface{}) bool {
	if reflect.TypeOf(k1) != reflect.TypeOf(k2) {
		return false
	}

	if reflect.ValueOf(k1).Kind() == reflect.Func {
		return &k1 == &k2
	}

	if k1 == k2 {
		return true
	}

	switch k1 := k1.(type) {
	case string:
		return k1 == k2.(string)

	case int64:
		return k1 == k2.(int64)

	case int32:
		return k1 == k2.(int32)

	case int16:
		return k1 == k2.(int16)

	case int8:
		return k1 == k2.(int8)

	case int:
		return k1 == k2.(int)

	case float32:
		return k1 == k2.(float32)

	case float64:
		return k1 == k2.(float64)

	case uint:
		return k1 == k2.(uint)

	case uint8:
		return k1 == k2.(uint8)

	case uint16:
		return k1 == k2.(uint16)

	case uint32:
		return k1 == k2.(uint32)

	case uint64:
		return k1 == k2.(uint64)

	case uintptr:
		return k1 == k2.(uintptr)
	}

	return false
}
