package amf

import (
        "bufio"
        "bytes"
        "encoding/binary"
        "fmt"
        "os"
        "http"
        "strconv"
        "reflect"
        )

// For in-progress code:
func unused(... interface{}) { }

// Interface for the local Reader. This can be implemented by bufio.Reader.
type Reader interface {
    Read(p []byte) (n int, err os.Error)
    Peek(n int) ([]byte, os.Error)
}

type Writer interface {
    Write(p []byte) (n int, err os.Error)
}

// Type markers
const (
    amf0_numberType = 0
    amf0_booleanType = 1
    amf0_stringType = 2
    amf0_objectType = 3
    amf0_movieClipType = 4
    amf0_nullType = 5
    amf0_undefinedType = 6
    amf0_referenceType = 7
    amf0_ecmaArrayType = 8
    amf0_objectEndType = 9
    amf0_strictArrayType = 10
    amf0_dateType = 11
    amf0_longStringType = 12
    amf0_unsupporedType = 13
    amf0_recordsetType = 14
    amf0_xmlObjectType = 15
    amf0_typedObjectType = 16
    amf0_avmPlusObjectType = 17

    amf3_undefinedType = 0
    amf3_nullType = 1
    amf3_falseType = 2
    amf3_trueType = 3
    amf3_integerType = 4
    amf3_doubleType = 5
    amf3_stringType = 6
    amf3_xmlType = 7
    amf3_dateType = 8
    amf3_arrayType = 9
    amf3_objectType = 10
    amf3_avmPlusXmlType = 11
    amf3_byteArrayType = 12
)

type DecodeContext struct {
    amfVersion uint16
    useAmf3 bool

    stringTable []string
    classTable []*AvmClass
    objectTable []*AvmObject
}

type MessageBundle struct {
    amfVersion uint16
    headers []AmfHeader
    messages []AmfMessage
}

type AmfHeader struct {
    name string
    mustUnderstand bool
    value AvmValue
}
type AmfMessage struct {
    targetUri string
    responseUri string
    args []AvmValue
    body AvmValue
}

// AvmValue can be a Go type, or AvmObject.
type AvmValue interface {
}

type AvmObject struct {
    class *AvmClass
}

type AvmClass struct {
    name string
    externalizable bool
    dynamic bool
    properties []string
}

// Helper functions.
func readByte(stream Reader) (uint8, os.Error) {
    buf := make([]byte, 1)
    _, err := stream.Read(buf)
    return buf[0], err
}

func readUint8(stream Reader) uint8 {
    var value uint8
    binary.Read(stream, binary.BigEndian, &value)
    return value
}
func readUint16(stream Reader) uint16 {
    var value uint16
    binary.Read(stream, binary.BigEndian, &value)
    return value
}
func writeUint16(stream Writer, value uint16) {
    binary.Write(stream, binary.BigEndian, &value)
}
func readUint32(stream Reader) uint32 {
    var value uint32
    binary.Read(stream, binary.BigEndian, &value)
    return value
}
func writeUint32(stream Writer, value uint32) {
    binary.Write(stream, binary.BigEndian, &value)
}
func readFloat64(stream Reader) float64 {
    var value float64
    binary.Read(stream, binary.BigEndian, &value)
    return value
}
func readString(stream Reader) string {
    length := readUint16(stream)
    data := make([]byte, length)
    stream.Read(data)
    return string(data)
}
func readStringLength(stream Reader, length int) string {
    data := make([]byte, length)
    stream.Read(data)
    return string(data)
}
func peekByte(stream Reader) uint8 {
    buf, _ := stream.Peek(1)
    return buf[0]
}
func writeString(stream Writer, str string) {
    binary.Write(stream, binary.BigEndian, uint16(len(str)))
}
func writeByte(stream Writer, b uint8) {
    binary.Write(stream, binary.BigEndian, b)
}
func writeBool(stream Writer, b bool) {
    val := 0x0
    if b {
        val = 0xff
    }
    binary.Write(stream, binary.BigEndian, uint8(val))
}

// Read a 29-bit compact encoded integer (as defined in AVM3)
func readUint29(stream Reader) (uint32, os.Error) {
    var result uint32 = 0
    for i := 0; i < 4; i++ {
        b, err := readByte(stream)

        if err != nil {
            return result, err
        }

        result = (result << 7) + (uint32(b) & 0x7f)

        if (b & 0x80) == 0 {
            break
        }
    }
    return result, nil
}

func writeUint29(stream Writer, value uint32) {
    // TODO
}

func readStringAmf3(stream Reader, cxt *DecodeContext) (string, os.Error) {
    ref,_ := readUint29(stream)

    // Check the low bit to see if this is a reference
    if (ref & 1) == 0 {
        return cxt.stringTable[int(ref>>1)],nil
    }

    length := int(ref >> 1)

    if (length == 0) {
        return "", nil
    }

    str := readStringLength(stream, length)

    cxt.stringTable = append(cxt.stringTable, str)

    return str, nil
}

func writeStringAmf3(stream Writer, s string) {
    length := len(s)

    // TODO: Support references
    writeUint29(stream, uint32((length << 1) + 1))

    stream.Write([]byte(s))
}

func readObjectAmf3(stream Reader, cxt *DecodeContext) (*AvmObject, os.Error) {

    ref,_ := readUint29(stream)

    fmt.Printf("in readObjectAmf3, parsed ref: %d\n", ref)

    // Check the low bit to see if this is a reference
    if (ref & 1) == 0 {
        return cxt.objectTable[int(ref >> 1)], nil
    }

    class,_ := readClassDefinitionAmf3(stream, ref, cxt)

    object := AvmObject{class}

    // Store the object in the table before doing any decoding, in case of
    // circular references.
    cxt.objectTable = append(cxt.objectTable, &object)

    var fields map[string] interface{}

    // Read static fields
    for _,propName := range class.properties {
        value := readValueAmf3(stream, cxt)
        fields[propName] = value
        fmt.Printf("read static field %s = %s", propName, value)
    }

    if class.dynamic {
        // Parse dynamic fields
        for {
            name,_ := readStringAmf3(stream, cxt)
            if name == "" {
                break
            }

            value := readValueAmf3(stream, cxt)
            fields[name] = value
        }
    }

    return &object,nil
}

func writeObjectAmf3(stream Writer, value interface{}) os.Error {

    fmt.Printf("writeValueAmf3 attempting to write a value of type %s",
        reflect.ValueOf(value).Type().Name())

    return nil
}

func readClassDefinitionAmf3(stream Reader, ref uint32, cxt *DecodeContext) (*AvmClass, os.Error) {

    // Check for a reference to an existing class definition
    if (ref & 2) == 0 {
        return cxt.classTable[int(ref >> 2)], nil
    }

    // Parse a class definition
    className,_ := readStringAmf3(stream, cxt)
    fmt.Printf("read className = %s\n", className)

    externalizable := ref & 4 != 0
    dynamic := ref & 8 != 0
    propertyCount := ref >> 4

    unused(externalizable, dynamic)

    fmt.Printf("read propertyCount = %d\n", propertyCount)

    class := AvmClass{className, externalizable, dynamic, make([]string, propertyCount)}

    // Property names
    for i := uint32(0); i < propertyCount; i++ {
        class.properties[i],_ = readStringAmf3(stream, cxt)
        fmt.Printf("read property: %s\n", class.properties[i])
    }

    // Save the new class in the loopup table
    cxt.classTable = append(cxt.classTable, &class)

    return &class, nil
}

func readArrayAmf3(stream Reader, cxt *DecodeContext) (interface{}, os.Error) {
    ref,_ := readUint29(stream)

    fmt.Printf("readArrayAmf3 read ref: %d\n", ref)

    // Check the low bit to see if this is a reference
    if (ref & 1) == 0 {
        return cxt.objectTable[int(ref >> 1)], nil
    }

    size := int(ref >> 1)

    fmt.Printf("readArrayAmf3 read size: %d", size)

    key,_ := readStringAmf3(stream, cxt)

    if key == "" {
        // No key, the whole array is dense.
        result := make([]interface{}, size)

        for i := 0; i < size; i++ {
            result[size] = readValueAmf3(stream, cxt)
        }
        return result, nil
    }

    // There are keys, return a mixed array.
    // TODO

    unused(size)

    return nil,nil
}

func readRequestArgs(stream Reader, cxt *DecodeContext) []AvmValue {
    lookaheadByte := peekByte(stream)
    if lookaheadByte == 17 {
        if !cxt.useAmf3 {
            fmt.Printf("Unexpected AMF3 type with incorrect message type")
        }
        fmt.Printf("while reading args, found next byte of 17")
        return nil
    }

    if lookaheadByte != 10 {
        fmt.Printf("Strict array type required for request body (found %d)", lookaheadByte)
        return nil
    }

    readByte(stream)

    count := readUint32(stream)
    result := make([]AvmValue, count)

    fmt.Printf("argument count = %d\n", count)

    for i := uint32(0); i < count; i++ {
        result[i] = readValue(stream, cxt)
        fmt.Printf("parsed value %s", result[i])
    }
    return result
}

func writeRequestArgs(stream Writer, message *AmfMessage) os.Error {

    writeUint32(stream, uint32(len(message.args)))

    for _,arg := range message.args {
        writeValueAmf3(stream, arg)
    }
    return nil
}

func readValue(stream Reader, cxt *DecodeContext) AvmValue {
    if cxt.amfVersion == 0 {
        return readValueAmf0(stream, cxt)
    }

    return readValueAmf3(stream, cxt)
}

func readValueAmf0(stream Reader, cxt *DecodeContext) AvmValue {
    typeMarker,_ := readByte(stream)

    // Type markers
    switch typeMarker {
    case amf0_numberType:
        return readFloat64(stream)
    case amf0_booleanType:
        return readUint8(stream) != 0
    case amf0_stringType:
        return readString(stream)
    case amf0_objectType:
        result := map[string]interface{} {}
        for true {
            c1,_ := readByte(stream)
            c2,_ := readByte(stream)
            length := int(c1) << 8 + int(c2)
            name := readStringLength(stream, length)
            k := peekByte(stream)
            if k == 0x09 {
                break
            }
            result[name] = readValueAmf0(stream, cxt)
        }
        return result

    case amf0_movieClipType:
        fmt.Printf("movie clip type not supported")
    case amf0_nullType:
        return nil
    case amf0_undefinedType:
    case amf0_referenceType:
    case amf0_ecmaArrayType:
    case amf0_objectEndType:
    case amf0_strictArrayType:
    case amf0_dateType:
    case amf0_longStringType:
    case amf0_unsupporedType:
    case amf0_recordsetType:
    case amf0_xmlObjectType:
    case amf0_typedObjectType:
    case amf0_avmPlusObjectType:
        return readValueAmf3(stream, cxt)
    }

    fmt.Printf("AMF0 type marker was not supported: %d", typeMarker)
    return nil
}

func readValueAmf3(stream Reader, cxt *DecodeContext) AvmValue {
    typeMarker,_ := readByte(stream)

    // Seems like there are some cases where we expect an AMF3 value and we get
    // the AMF0 typeCode of amf0_avmPlusObjectType. At least this is unambiguous.
    if typeMarker == amf0_avmPlusObjectType {
        typeMarker,_ = readByte(stream)
    }

    fmt.Printf("read typeMarker: %d\n", typeMarker)

    switch typeMarker {
    case amf3_nullType:
        return nil
    case amf3_falseType:
        return false
    case amf3_trueType:
        return true
    case amf3_integerType:
        result,_ := readUint29(stream)
        return result
    case amf3_doubleType:
        return readFloat64(stream)
    case amf3_stringType:
        result,_ := readStringAmf3(stream, cxt)
        return result
    case amf3_objectType:
        result,_ := readObjectAmf3(stream, cxt)
        return result
    case amf3_arrayType:
        result,_ := readArrayAmf3(stream, cxt)
        return result
    }

    fmt.Printf("AMF3 type marker was not supported: %d\n", typeMarker)
    return nil
}

func writeValueAmf3(stream Writer, value interface{}) os.Error {

    switch t := value.(type) {
    case string:
        writeByte(stream, amf3_stringType)
        str,_ := value.(string)
        writeStringAmf3(stream, str)
    case int:
        writeByte(stream, amf3_integerType)
        n,_ := value.(uint32)
        writeUint29(stream, n)
    case bool:
        if value == false {
            writeByte(stream, amf3_falseType)
        } else {
            writeByte(stream, amf3_trueType)
        }
    case nil:
        writeByte(stream, amf3_nullType)
    case []interface{}:
        writeByte(stream, amf3_arrayType)
        // TODO: Array type
    default:
        writeByte(stream, amf3_objectType)
        writeObjectAmf3(stream, value)
    }

    return nil
}

func Decode(stream Reader) (*MessageBundle, os.Error) {

    cxt := DecodeContext{}
    result := MessageBundle{}

    cxt.amfVersion = readUint16(stream)
    result.amfVersion = cxt.amfVersion

    fmt.Printf("amfVersion = %d\n", cxt.amfVersion)

    // see http://osflash.org/documentation/amf/envelopes/remoting#preamble
    // why we are doing this...
    if cxt.amfVersion > 0x09 {
        return nil, os.NewError("Malformed stream (wrong amfVersion)")
    }

    cxt.useAmf3 = cxt.amfVersion == 3
    headerCount := readUint16(stream)

    fmt.Printf("headerCount = %d\n", headerCount)

    // Read headers
    result.headers = make([]AmfHeader, headerCount)
    for i := 0; i < int(headerCount); i++ {
        name := readString(stream)
        mustUnderstand := readUint8(stream) != 0
        messageLength := readUint32(stream)
        unused(messageLength)

        // Check for AMF3 type marker
        if (cxt.useAmf3) {
            typeMarker := peekByte(stream)
            if typeMarker == 17 {
                fmt.Printf("found AMF3 type marker on header")
            }
        }

        value := readValue(stream, &cxt)
        header := AmfHeader{name, mustUnderstand, value}
        result.headers[i] = header

        fmt.Printf("Read header, name = %s", name)
    }

    // Read message bodies
    messageCount := readUint16(stream)
    fmt.Printf("messageCount = %d\n", messageCount)
    result.messages = make([]AmfMessage, messageCount)

    for i := 0; i < int(messageCount); i++ {

        message := &result.messages[i]

        message.targetUri = readString(stream)
        message.responseUri = readString(stream)

        messageLength := readUint32(stream)

        fmt.Printf("Read targetUri = %s\n", message.targetUri)
        fmt.Printf("Read responseUri = %s\n", message.responseUri)
        fmt.Printf("Read messageLength = %d\n", messageLength)

        is_request := true
        if is_request {
            readRequestArgs(stream, &cxt)
        }

        message.body = readValue(stream, &cxt)

        unused(messageLength)
    }

    return &result, nil
}

func Encode(stream Writer, bundle *MessageBundle) os.Error {
    writeUint16(stream, bundle.amfVersion)

    // Write headers
    writeUint16(stream, uint16(len(bundle.headers)))
    for _, header := range bundle.headers {
        writeString(stream, header.name)
        writeBool(stream, header.mustUnderstand)
    }

    // Write messages
    writeUint16(stream, uint16(len(bundle.messages)))
    for _, message := range bundle.messages {
        writeString(stream, message.targetUri)
        writeString(stream, message.responseUri)
        writeUint32(stream, 0)

        writeRequestArgs(stream, &message)

        writeValueAmf3(stream, message.body)
    }

    return nil
}

func handleGet(w http.ResponseWriter) {
    w.Header().Set("Content-Type", "text/plain")
    w.WriteHeader(405)
    fmt.Fprintf(w, "405 Method Not Allowed\n\n"+
            "To access this amfgo gateway you must use POST requests "+
            "(%s received))")
}

func writeReply500(w http.ResponseWriter) {
    w.Header().Set("Content-Type", "text/plain")
    w.WriteHeader(500)
    fmt.Fprintf(w, "500 Internal Server Error\n\n"+
            "Unexplained error")
}

type FlexRemotingMessage struct {
    operation string
    source string
}

type FlexCommandMessage struct {
    operation int
    messageRefType string
    //AUTHENTICATION_MESSAGE_REF_TYPE = "flex.messaging.messages.AuthenticationMessage"
}

type FlexAcknowledgeMessage struct {
    flags byte
}

type FlexErrorMessage struct {
    extendedData string
    faultCode int
    faultDetail int
    faultString string
    rootCause string
}

func handler(w http.ResponseWriter, r *http.Request) {
    if r.Method == "Get" {
        handleGet(w)
        return
    }

    // Decode the request
    request,_ := Decode(bufio.NewReader(r.Body))

    unused(request)

    reply := MessageBundle{}
    reply.amfVersion = 3
    reply.messages = make([]AmfMessage, 1)

    reply.messages[0].args = []AvmValue{"hello"}

    replyBuffer := bytes.NewBuffer(make([]byte, 0))
    Encode(replyBuffer, &reply)
    replyData := replyBuffer.Bytes()

    fmt.Printf("reply data has size: %d", len(replyData))

    w.Header().Set("Content-Type", "application/x-amf")
    w.Header().Set("Content-Length", strconv.Itoa(len(replyData)))
    w.Header().Set("Server", "SERVER_NAME")

    w.Write(replyData)

    fmt.Printf("writing reply data: %s", replyData)

    // remoting.decode

    // process request (to callback)

    // remoting.encode

    //writeReply500(w)

}
func main() {
    http.HandleFunc("/", handler)
    http.ListenAndServe(":8080", nil)
}