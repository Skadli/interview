package main

import (
	"bytes"
	"compress/gzip"
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"io"
	"math"
)

// 大端 int32 / uint32
func i32(v int32) []byte  { b := make([]byte, 4); binary.BigEndian.PutUint32(b, uint32(v)); return b }
func u32be(v uint32) []byte { b := make([]byte, 4); binary.BigEndian.PutUint32(b, v); return b }

func gz(b []byte) []byte {
	var buf bytes.Buffer
	w := gzip.NewWriter(&buf)
	_, _ = w.Write(b)
	_ = w.Close()
	return buf.Bytes()
}

func gunzip(b []byte) []byte {
	r, err := gzip.NewReader(bytes.NewReader(b))
	if err != nil {
		return b
	}
	defer r.Close()
	out, _ := io.ReadAll(r)
	return out
}

func uuid4() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// int16 PCM <-> 小端字节
func pcmToBytes(pcm []int16) []byte {
	b := make([]byte, len(pcm)*2)
	for i, v := range pcm {
		binary.LittleEndian.PutUint16(b[i*2:], uint16(v))
	}
	return b
}

func bytesToPCM(b []byte) []int16 {
	n := len(b) / 2
	out := make([]int16, n)
	for i := 0; i < n; i++ {
		out[i] = int16(binary.LittleEndian.Uint16(b[i*2:]))
	}
	return out
}

func rmsI16(w []int16) float64 {
	var s float64
	for _, v := range w {
		f := float64(v) / 32768.0
		s += f * f
	}
	return math.Sqrt(s/float64(len(w)) + 1e-12)
}

func msOf(sample int64, sr int) int64 { return sample * 1000 / int64(sr) }

func round3(f float64) float64 { return math.Round(f*1000) / 1000 }

func isQuestion(text string) bool {
	t := []rune(text)
	if len(t) < 3 {
		return false
	}
	kw := []string{"?", "？", "吗", "呢", "为什么", "如何", "怎么", "怎样", "谈谈", "说说",
		"介绍", "请", "什么", "是否", "看法", "评价", "如果", "为何", "聊聊"}
	for _, k := range kw {
		if bytes.Contains([]byte(text), []byte(k)) {
			return true
		}
	}
	return false
}
