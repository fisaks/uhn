package util
import "strings"

func BytesToBinaryString(bs []byte, count int) string {
    var s strings.Builder
    bitsAdded := 0
    for _, b := range bs {
        for i := 0; i < 8 && bitsAdded < count; i++ {
            if b&(1<<i) != 0 {
                s.WriteString("1")
            } else {
                s.WriteString("0")
            }
            bitsAdded++
        }
    }
    return s.String()
}


func IntToBinaryString(words []uint16, count int) string {
    var s strings.Builder
    bitsAdded := 0
    for _, w := range words {
        for i := 0; i < 16 && bitsAdded < count; i++ {
            if w&(1<<i) != 0 {
                s.WriteString("1")
            } else {
                s.WriteString("0")
            }
            bitsAdded++
        }
    }
    return s.String()
}