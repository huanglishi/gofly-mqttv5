package service

import (
	"bufio"
	"fmt"
	"io"
	"math"
	"sync"
	"sync/atomic"

	logger2 "github.com/huanglishi/gofly-mqttv5/logger"
)

var (
	bufcnt int64
)

var (
	defaultBufferSize     int64 = 1024 * 256 //comment.DefaultBufferSize
	defaultReadBlockSize  int64 = 1024       //comment.DefaultReadBlockSize
	defaultWriteBlockSize int64 = 1024       //comment.DefaultWriteBlockSize
)

// buff 配置设置
func QUICBuffConfigInit(bufferSize, readBlockSize, writeBlockSize uint64) {
	if bufferSize > math.MaxInt64 {
		panic("bufferSize more than math.MaxInt64")
	}
	if readBlockSize > math.MaxInt64 {
		panic("readBlockSize more than math.MaxInt64")
	}
	if writeBlockSize > math.MaxInt64 {
		panic("writeBlockSize more than math.MaxInt64")
	}
	defaultBufferSize = int64(bufferSize)
	defaultReadBlockSize = int64(readBlockSize)
	defaultWriteBlockSize = int64(writeBlockSize)
}

type sequence struct {
	// The current position of the producer or consumer
	//生产者或消费者的当前地位
	cursor,

	// The previous known position of the consumer (if producer) or producer (if consumer)
	//消费者(如果生产者)或生产者(如果消费者)以前的已知位置
	gate,

	// These are fillers to pad the cache line, which is generally 64 bytes
	//这些是填充缓存线路的填充材料，通常为64字节
	p2, p3, p4, p5, p6, p7 int64
}

func newSequence() *sequence {
	return &sequence{}
}

func (this *sequence) get() int64 {
	return atomic.LoadInt64(&this.cursor)
}

func (this *sequence) set(seq int64) {
	atomic.StoreInt64(&this.cursor, seq)
}

type buffer struct {
	id int64

	buf []byte
	// 临时数据，用于读取buf，而不移动位置
	tmp []byte

	size int64
	mask int64

	done int64

	pseq *sequence
	cseq *sequence

	pcond *sync.Cond
	ccond *sync.Cond

	cwait int64
	pwait int64
}

func newBuffer(size int64) (*buffer, error) {
	if size < 0 {
		return nil, bufio.ErrNegativeCount
	}

	if size == 0 {
		size = defaultBufferSize
	}
	if !powerOfTwo64(size) {
		return nil, fmt.Errorf("Size must be power of two. Try %d.", roundUpPowerOfTwo64(size))
	}

	if size/2 < defaultReadBlockSize {
		return nil, fmt.Errorf("Size must at least be %d. Try %d.", 2*defaultReadBlockSize, 2*defaultReadBlockSize)
	}
	// TODO 这里一次性分配有点多，可以优化
	// 虽然可以减少之后的扩容重新分配的次数，但是还是太占内存了，而且又不能自动缩容
	return &buffer{
		id:    atomic.AddInt64(&bufcnt, 1),
		buf:   make([]byte, size),
		size:  size,
		mask:  size - 1,
		pseq:  newSequence(),
		cseq:  newSequence(),
		pcond: sync.NewCond(new(sync.Mutex)),
		ccond: sync.NewCond(new(sync.Mutex)),
		cwait: 0,
		pwait: 0,
	}, nil
}

func (this *buffer) ID() int64 {
	return this.id
}

func (this *buffer) Close() error {
	atomic.StoreInt64(&this.done, 1)

	this.pcond.L.Lock()
	this.pcond.Broadcast()
	this.pcond.L.Unlock()

	this.pcond.L.Lock()
	this.ccond.Broadcast()
	this.pcond.L.Unlock()

	return nil
}

func (this *buffer) Len() int {
	cpos := this.cseq.get()
	ppos := this.pseq.get()
	return int(ppos - cpos)
}

func (this *buffer) ReadFrom(r io.Reader) (int64, error) {
	defer this.Close()

	total := int64(0)

	for {
		if this.isDone() {
			return total, io.EOF
		}

		start, cnt, err := this.waitForWriteSpace(int(defaultReadBlockSize))
		if err != nil {
			return 0, err
		}

		pstart := start & this.mask
		pend := pstart + int64(cnt)
		if pend > this.size {
			pend = this.size
		}

		n, err := r.Read(this.buf[pstart:pend])
		if n > 0 {
			total += int64(n)
			_, err := this.WriteCommit(n)
			if err != nil {
				return total, err
			}
		}

		if err != nil {
			return total, err
		}
	}
}

func (this *buffer) WriteTo(w io.Writer) (int64, error) {
	defer func() {
		this.Close()
	}()

	total := int64(0)

	for {
		if this.isDone() {
			return total, io.EOF
		}

		p, err := this.ReadPeek(int(defaultWriteBlockSize))
		//if err != nil {
		//	return 0, err
		//}
		// There's some data, let's process it first
		if len(p) > 0 {
			n, err := w.Write(p)
			total += int64(n)
			logger2.Logger.Debugf("Wrote %d bytes, totaling %d bytes", n, total)

			if err != nil {
				return total, err
			}

			_, err = this.ReadCommit(n)
			if err != nil {
				return total, err
			}
		}

		if err != ErrBufferInsufficientData && err != nil {
			return total, err
		}
	}
}

func (this *buffer) Read(p []byte) (int, error) {
	if this.isDone() && this.Len() == 0 {
		//glog.Debugf("isDone and len = %d", this.Len())
		return 0, io.EOF
	}

	pl := int64(len(p))

	for {
		cpos := this.cseq.get()
		ppos := this.pseq.get()
		cindex := cpos & this.mask

		// If consumer position is at least len(p) less than producer position, that means
		// we have enough data to fill p. There are two scenarios that could happen:
		// 1. cindex + len(p) < buffer size, in this case, we can just copy() data from
		//    buffer to p, and copy will just copy enough to fill p and stop.
		//    The number of bytes copied will be len(p).
		// 2. cindex + len(p) > buffer size, this means the data will wrap around to the
		//    the beginning of the buffer. In thise case, we can also just copy data from
		//    buffer to p, and copy will just copy until the end of the buffer and stop.
		//    The number of bytes will NOT be len(p) but less than that.
		//如果消费者至少是len(p)小于生产者，这意味着
		//我们有足够的数据来填充p。有两种可能发生的情况:
		// 1。cindex + len(p) < buffer size，在这种情况下，我们可以复制()数据
		//缓冲到p，复制就会复制到足够填充p并停止。
		//复制的字节数是len(p)。
		// 2。cindex + len(p) >缓冲区的大小，这意味着数据会绕到
		//缓冲区的开始。在这种情况下，我们也可以复制数据
		//缓冲到p，然后复制，一直复制到缓冲区的末尾，然后停止。
		//字节数不是len(p)，而是小于len(p)。
		if cpos+pl < ppos {
			n := copy(p, this.buf[cindex:])

			this.cseq.set(cpos + int64(n))
			this.pcond.L.Lock()
			this.pcond.Broadcast()
			this.pcond.L.Unlock()

			return n, nil
		}

		// If we got here, that means there's not len(p) data available, but there might
		// still be data.
		//如果我们到这里，这意味着没有可用的len(p)数据，但可能有
		//仍然是数据。
		// If cpos < ppos, that means there's at least ppos-cpos bytes to read. Let's just
		// send that back for now.
		//如果cpos < ppos，那就意味着至少要读取ppos-cpos字节。我们就
		//现在就把它寄回去。
		if cpos < ppos {
			// n bytes available
			b := ppos - cpos

			// bytes copied
			var n int

			// if cindex+n < size, that means we can copy all n bytes into p.
			// No wrapping in this case.
			//如果cindex+n < size，这意味着我们可以将所有的n个字节复制到p中。
			//这种箱子不用包装。
			if cindex+b < this.size {
				n = copy(p, this.buf[cindex:cindex+b])
			} else {
				// If cindex+n >= size, that means we can copy to the end of buffer
				//如果cindex+n >= size，这意味着我们可以复制到缓冲区的末尾
				n = copy(p, this.buf[cindex:])
			}

			this.cseq.set(cpos + int64(n))
			this.pcond.L.Lock()
			this.pcond.Broadcast()
			this.pcond.L.Unlock()
			return n, nil
		}

		// If we got here, that means cpos >= ppos, which means there's no data available.
		// If so, let's wait...
		//如果我们到这里，这意味着cpos >= ppos，这意味着没有可用的数据。
		//如果是这样的话，我们等着……
		this.ccond.L.Lock()
		for ppos = this.pseq.get(); cpos >= ppos; ppos = this.pseq.get() {
			if this.isDone() {
				return 0, io.EOF
			}

			this.cwait++
			this.ccond.Wait()
		}
		this.ccond.L.Unlock()
	}
}

func (this *buffer) Write(p []byte) (int, error) {
	if this.isDone() {
		return 0, io.EOF
	}

	start, _, err := this.waitForWriteSpace(len(p))
	if err != nil {
		return 0, err
	}

	// If we are here that means we now have enough space to write the full p.
	// Let's copy from p into this.buf, starting at position ppos&this.mask.
	//如果我们在这里，这意味着我们现在有足够的空间来写完整的p。
	//我们把p复制到这里。buf，从ppos&this.mask开始。
	total := ringCopy(this.buf, p, int64(start)&this.mask)

	this.pseq.set(start + int64(len(p)))
	this.ccond.L.Lock()
	this.ccond.Broadcast()
	this.ccond.L.Unlock()

	return total, nil
}

// Description below is copied completely from bufio.Peek()
//
//	http://golang.org/pkg/bufio/#Reader.Peek
//
// Peek returns the next n bytes without advancing the reader. The bytes stop being valid
// at the next read call. If Peek returns fewer than n bytes, it also returns an error
// explaining why the read is short. The error is bufio.ErrBufferFull if n is larger than
// b's buffer size.
// If there's not enough data to peek, error is ErrBufferInsufficientData.
// If n < 0, error is bufio.ErrNegativeCount
// 下面的描述完全复制自bufio.Peek()
// http://golang.org/pkg/bufio/ Reader.Peek
// Peek返回接下来的n个字节，而不提前读取器。字节不再有效
// 在下一个read调用时。如果Peek返回的字节数小于n，那么它也会返回一个错误
// 解释为什么读得短。错误是bufio.ErrBufferFull如果n大于
// b的缓冲区大小。
// 如果没有足够的数据供查看，则错误为ErrBufferInsufficientData。
// 如果n < 0，错误是bufio.ErrNegativeCount
func (this *buffer) ReadPeek(n int) ([]byte, error) {
	if int64(n) > this.size {
		return nil, bufio.ErrBufferFull
	}

	if n < 0 {
		return nil, bufio.ErrNegativeCount
	}

	cpos := this.cseq.get()
	ppos := this.pseq.get()

	// If there's no data, then let's wait until there is some data
	//如果没有数据，那就等到有数据的时候
	this.ccond.L.Lock()
	for ; cpos >= ppos; ppos = this.pseq.get() {
		if this.isDone() {
			return nil, io.EOF
		}

		this.cwait++
		this.ccond.Wait()
	}
	this.ccond.L.Unlock()

	// m = the number of bytes available. If m is more than what's requested (n),
	// then we make m = n, basically peek max n bytes
	// m =可用字节数。如果m大于要求的量(n)，
	//然后我们令m = n，也就是偷看最多n个字节
	m := ppos - cpos
	err := error(nil)

	if m >= int64(n) {
		m = int64(n)
	} else {
		err = ErrBufferInsufficientData
	}

	// There's data to peek. The size of the data could be <= n.
	//有数据可以看。数据的大小可以是<= n。
	if cpos+m <= ppos {
		cindex := cpos & this.mask

		// If cindex (index relative to buffer) + n is more than buffer size, that means
		// the data wrapped
		//如果cindex(相对于缓冲区的索引)+ n大于缓冲区大小，这意味着
		//数据包装
		if cindex+m > this.size {
			// reset the tmp buffer
			this.tmp = this.tmp[0:0]

			l := len(this.buf[cindex:])
			this.tmp = append(this.tmp, this.buf[cindex:]...)
			// 不足，就从前面读取
			this.tmp = append(this.tmp, this.buf[0:m-int64(l)]...)
			return this.tmp, err
		} else {
			return this.buf[cindex : cindex+m], err
		}
	}

	return nil, ErrBufferInsufficientData
}

// Wait waits for for n bytes to be ready. If there's not enough data, then it will
// wait until there's enough. This differs from ReadPeek or Readin that Peek will
// return whatever is available and won't wait for full count.
// Wait等待n个字节的数据准备好。如果没有足够的数据，它就会
// 等到足够了。这与ReadPeek或Readin的那个Peek不同
// 返回所有可用的，不会等待完整计数。
func (this *buffer) ReadWait(n int) ([]byte, error) {
	if int64(n) > this.size {
		return nil, bufio.ErrBufferFull
	}

	if n < 0 {
		return nil, bufio.ErrNegativeCount
	}

	cpos := this.cseq.get()
	ppos := this.pseq.get()

	// This is the magic read-to position. The producer position must be equal or
	// greater than the next position we read to.
	//这就是神奇的“读到”位置。生产者位置必须等于或
	//比我们读到的下一个位置更大。
	next := cpos + int64(n)

	// If there's no data, then let's wait until there is some data
	this.ccond.L.Lock()
	for ; next > ppos; ppos = this.pseq.get() {
		if this.isDone() {
			return nil, io.EOF
		}

		this.ccond.Wait()
	}
	this.ccond.L.Unlock()

	// If we are here that means we have at least n bytes of data available.
	//如果我们在这里，这意味着我们至少有n个字节的可用数据。
	cindex := cpos & this.mask

	// If cindex (index relative to buffer) + n is more than buffer size, that means
	// the data wrapped
	//如果cindex(相对于缓冲区的索引)+ n大于缓冲区大小，这意味着
	//数据包装，因为超过了buf大小
	if cindex+int64(n) > this.size {
		// reset the tmp buffer
		this.tmp = this.tmp[0:0]

		l := len(this.buf[cindex:])
		this.tmp = append(this.tmp, this.buf[cindex:]...)
		this.tmp = append(this.tmp, this.buf[0:n-l]...)
		return this.tmp[:n], nil
	}

	return this.buf[cindex : cindex+int64(n)], nil
}

// Commit moves the cursor forward by n bytes. It behaves like Read() except it doesn't
// return any data. If there's enough data, then the cursor will be moved forward and
// n will be returned. If there's not enough data, then the cursor will move forward
// as much as possible, then return the number of positions (bytes) moved.
// Commit将光标向前移动n个字节。它的行为类似于Read()，但它不是
// 返回任何数据。如果有足够的数据，那么光标将向前移动
// n将被返回。如果没有足够的数据，那么光标将向前移动
// 尽可能多地返回移动的位置(字节)数量。
func (this *buffer) ReadCommit(n int) (int, error) {
	if int64(n) > this.size {
		return 0, bufio.ErrBufferFull
	}

	if n < 0 {
		return 0, bufio.ErrNegativeCount
	}

	cpos := this.cseq.get()
	ppos := this.pseq.get()

	// If consumer position is at least n less than producer position, that means
	// we have enough data to fill p. There are two scenarios that could happen:
	// 1. cindex + n < buffer size, in this case, we can just copy() data from
	//    buffer to p, and copy will just copy enough to fill p and stop.
	//    The number of bytes copied will be len(p).
	// 2. cindex + n > buffer size, this means the data will wrap around to the
	//    the beginning of the buffer. In thise case, we can also just copy data from
	//    buffer to p, and copy will just copy until the end of the buffer and stop.
	//    The number of bytes will NOT be len(p) but less than that.
	if cpos+int64(n) <= ppos {
		this.cseq.set(cpos + int64(n))
		this.pcond.L.Lock()
		this.pcond.Broadcast()
		this.pcond.L.Unlock()
		return n, nil
	}

	return 0, ErrBufferInsufficientData
}

// WaitWrite waits for n bytes to be available in the buffer and then returns
// 1. the slice pointing to the location in the buffer to be filled
// 2. a boolean indicating whether the bytes available wraps around the ring
// 3. any errors encountered. If there's error then other return values are invalid
// WaitWrite等待缓冲区中可用的n个字节，然后返回
// 1。指向缓冲区中要填充的位置的片
// 2。 一个布尔值，指示可用的字节是否包围环
// 3。 遇到任何错误。 如果出现错误，则其他返回值无效
func (this *buffer) WriteWait(n int) ([]byte, bool, error) {
	start, cnt, err := this.waitForWriteSpace(n)
	if err != nil {
		return nil, false, err
	}

	pstart := start & this.mask
	if pstart+int64(cnt) > this.size {
		return this.buf[pstart:], true, nil
	}

	return this.buf[pstart : pstart+int64(cnt)], false, nil
}

func (this *buffer) WriteCommit(n int) (int, error) {
	start, cnt, err := this.waitForWriteSpace(n)
	if err != nil {
		return 0, err
	}

	// If we are here then there's enough bytes to commit
	//如果我们在这里，那么有足够的字节提交
	this.pseq.set(start + int64(cnt))

	this.ccond.L.Lock()
	this.ccond.Broadcast()
	this.ccond.L.Unlock()

	return cnt, nil
}

// 等待写空间
func (this *buffer) waitForWriteSpace(n int) (int64, int, error) {
	if this.isDone() {
		return 0, 0, io.EOF
	}

	// The current producer position, remember it's a forever inreasing int64,
	// NOT the position relative to the buffer
	//当前的生产者位置，记住它是一个永远递增的int64，
	//不是相对于缓冲区的位置
	ppos := this.pseq.get()

	// The next producer position we will get to if we write len(p)
	//如果我们写入len(p)，我们将得到的下一个生产者位置
	next := ppos + int64(n)

	// For the producer, gate is the previous consumer sequence.
	//对于生产者，gate是前面的消费者序列。
	gate := this.pseq.gate

	wrap := next - this.size

	// If wrap point is greater than gate, that means the consumer hasn't read
	// some of the data in the buffer, and if we read in additional data and put
	// into the buffer, we would overwrite some of the unread data. It means we
	// cannot do anything until the customers have passed it. So we wait...
	//如果wrap point大于gate，表示消费者还没有读取
	//在buffer中读取一些数据，如果我们读入额外的数据并放入
	//写入缓冲区，我们将覆盖一些未读数据。这意味着我们
	//不能做任何事情，直到客户通过它。所以我们等待……
	//
	// Let's say size = 16, block = 4, ppos = 0, gate = 0
	//   then next = 4 (0+4), and wrap = -12 (4-16)
	//   _______________________________________________________________________
	//   | 0 | 1 | 2 | 3 | 4 | 5 | 6 | 7 | 8 | 9 | 10 | 11 | 12 | 13 | 14 | 15 |
	//   -----------------------------------------------------------------------
	//    ^                ^
	//    ppos,            next
	//    gate
	//
	// So wrap (-12) > gate (0) = false, and gate (0) > ppos (0) = false also,
	// so we move on (no waiting) //我们继续前行(不等待)
	//
	// Now if we get to ppos = 14, gate = 12,
	// then next = 18 (4+14) and wrap = 2 (18-16)
	//
	// So wrap (2) > gate (12) = false, and gate (12) > ppos (14) = false aos,
	// so we move on again //我们继续
	//
	// Now let's say we have ppos = 14, gate = 0 still (nothing read),
	// then next = 18 (4+14) and wrap = 2 (18-16)
	//
	// So wrap (2) > gate (0) = true, which means we have to wait because if we
	// put data into the slice to the wrap point, it would overwrite the 2 bytes
	// that are currently unread.
	//
	// Another scenario, let's say ppos = 100, gate = 80,
	// then next = 104 (100+4) and wrap = 88 (104-16)
	//
	// So wrap (88) > gate (80) = true, which means we have to wait because if we
	// put data into the slice to the wrap point, it would overwrite the 8 bytes
	// that are currently unread.
	//
	if wrap > gate || gate > ppos {
		var cpos int64
		this.pcond.L.Lock()
		for cpos = this.cseq.get(); wrap > cpos; cpos = this.cseq.get() {
			if this.isDone() {
				return 0, 0, io.EOF
			}

			this.pwait++
			this.pcond.Wait()
		}

		this.pseq.gate = cpos
		this.pcond.L.Unlock()
	}

	return ppos, n, nil
}

func (this *buffer) isDone() bool {
	if atomic.LoadInt64(&this.done) == 1 {
		return true
	}

	return false
}

func ringCopy(dst, src []byte, start int64) int {
	n := len(src)

	i, l := 0, 0

	for n > 0 {
		l = copy(dst[start:], src[i:])
		i += l
		n -= l

		if n > 0 {
			start = 0
		}
	}

	return i
}

func powerOfTwo64(n int64) bool {
	return n != 0 && (n&(n-1)) == 0
}

func roundUpPowerOfTwo64(n int64) int64 {
	n--
	n |= n >> 1
	n |= n >> 2
	n |= n >> 4
	n |= n >> 8
	n |= n >> 16
	n |= n >> 32
	n++

	return n
}
