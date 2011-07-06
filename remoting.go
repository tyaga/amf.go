
func readRequestArgs(stream Reader, cxt *DecodeContext) []AmfValue {
/*
    lookaheadByte := peekByte(stream)
    if lookaheadByte == 17 {
        if !cxt.useAmf3() {
            fmt.Printf("Unexpected AMF3 type with incorrect message type")
        }
        fmt.Printf("while reading args, found next byte of 17")
        return nil
    }

    if lookaheadByte != 10 {
        fmt.Printf("Strict array type required for request body (found %d)", lookaheadByte)
        return nil
    }
*/

    readByte(stream)

    count := readUint32(stream)
    result := make([]AmfValue, count)

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
        WriteValueAmf3(stream, arg)
    }
    return nil
}


func Decode(stream Reader) (*MessageBundle, os.Error) {

    cxt := DecodeContext{}
    result := MessageBundle{}

    cxt.amfVersion = readUint16(stream)
    result.amfVersion = cxt.amfVersion

    fmt.Printf("amfVersion = %d\n", cxt.amfVersion)

    /*
        From http://osflash.org/documentation/amf/envelopes/remoting:

        The first two bytes of an AMF message are an unsigned short int. The result 
        indicates what type of Flash Player connected to the server.

        0x00 for Flash Player 8 and below
        0x01 for FlashCom/FMS
        0x03 for Flash Player 9
        Note that Flash Player 9 will always set the second byte to 0x03, regardless of
        whether the message was sent in AMF0 or AMF3.
    */

    if cxt.amfVersion > 0x09 {
        return nil, os.NewError("Malformed stream (wrong amfVersion)")
    }

    headerCount := readUint16(stream)

    fmt.Printf("headerCount = %d\n", headerCount)

    /*
        From http://osflash.org/documentation/amf/envelopes/remoting:

        Each header consists of the following:

        UTF string (including length bytes) - name
        Boolean - specifies if understanding the header is 'required'
        Long - Length in bytes of header
        Variable - Actual data (including a type code)
    */

    // Read headers
    result.headers = make([]AmfHeader, headerCount)
    for i := 0; i < int(headerCount); i++ {
        name := ReadString(stream)
        mustUnderstand := readUint8(stream) != 0
        messageLength := readUint32(stream)
        unused(messageLength)

        // TODO: Check for AMF3 type marker?

        value := readValue(stream, &cxt)
        header := AmfHeader{name, mustUnderstand, value}
        result.headers[i] = header

        fmt.Printf("Read header, name = %s", name)
    }

    /*
        From http://osflash.org/documentation/amf/envelopes/remoting:
    
        Between the headers and the start of the bodies is a int specifying the number of
        bodies. Each body consists of the following:

        UTF String - Target
        UTF String - Response
        Long - Body length in bytes
        Variable - Actual data (including a type code)
    */

    // Read message bodies
    messageCount := readUint16(stream)
    fmt.Printf("messageCount = %d\n", messageCount)
    result.messages = make([]AmfMessage, messageCount)

    for i := 0; i < int(messageCount); i++ {
        // TODO: Should reset object tables here

        message := &result.messages[i]

        message.targetUri = ReadString(stream)
        message.responseUri = ReadString(stream)

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

func encodeBundle(stream Writer, bundle *MessageBundle) os.Error {
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

        WriteValueAmf3(stream, message.body)
    }

    return nil
}

// This gateway stuff will get moved to a separate file..

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
    requestBundle,_ := Decode(r.Body)

    // Initialize the reply bundle.
    replyBundle := MessageBundle{}
    replyBundle.amfVersion = 3
    replyBundle.messages = make([]AmfMessage, len(requestBundle.messages))

    // Construct a reply to each message.
    for index,request := range requestBundle.messages {
        reply := &replyBundle.messages[index]

        replyBody,success := amfMessageHandler(request)
        reply.body = replyBody

        /*
        From http://osflash.org/documentation/amf/envelopes/remoting:

        The response to a request has the exact same structure as a request. A request
        requiring a body response should be answered in the following way:

        Target: set to Response index plus one of "/onStatus", "onResult", or
        "/onDebugEvents". "/onStatus" is reserved for runtime errors. "/onResult" is for
        succesful calls. "/onDebugEvents" is for debug information, see debug information.
        Thus if the client requested something with response index '/1', and the call was
        succesful, '/1/onResult' should be sent back. Response: should be set to the string
        'null'.  Data: set to the returned data.
        */

        if success {
            reply.targetUri = request.targetUri + "/onResult"
        } else {
            reply.targetUri = request.targetUri + "/onStatus"
        }
        reply.responseUri = ""
        fmt.Printf("writing reply to message %d, targetUri = %s", index, reply.targetUri)
    }

    // Encode the outgoing message bundle.
    replyBuffer := bytes.NewBuffer(make([]byte, 0))
    encodeBundle(replyBuffer, &replyBundle)
    replyBytes := replyBuffer.Bytes()
    w.Write(replyBytes)

    w.Header().Set("Content-Type", "application/x-amf")
    w.Header().Set("Content-Length", strconv.Itoa(len(replyBytes)))
    w.Header().Set("Server", "SERVER_NAME")

    fmt.Printf("writing reply data with length: %d", len(replyBytes))
}

func amfMessageHandler(request AmfMessage) (data AmfValue, success bool) {
    return "hello", true
}

func main() {
    http.HandleFunc("/", handler)
    http.ListenAndServe(":8080", nil)
}
