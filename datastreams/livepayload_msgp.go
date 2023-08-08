package datastreams

// Code generated by github.com/tinylib/msgp DO NOT EDIT.

import (
	"github.com/tinylib/msgp/msgp"
)

// DecodeMsg implements msgp.Decodable
func (z *LivePayload) DecodeMsg(dc *msgp.Reader) (err error) {
	var field []byte
	_ = field
	var zb0001 uint32
	zb0001, err = dc.ReadMapHeader()
	if err != nil {
		err = msgp.WrapError(err)
		return
	}
	for zb0001 > 0 {
		zb0001--
		field, err = dc.ReadMapKeyPtr()
		if err != nil {
			err = msgp.WrapError(err)
			return
		}
		switch msgp.UnsafeString(field) {
		case "Message":
			z.Message, err = dc.ReadBytes(z.Message)
			if err != nil {
				err = msgp.WrapError(err, "Message")
				return
			}
		case "Topic":
			z.Topic, err = dc.ReadString()
			if err != nil {
				err = msgp.WrapError(err, "Topic")
				return
			}
		case "Partition":
			z.Partition, err = dc.ReadInt32()
			if err != nil {
				err = msgp.WrapError(err, "Partition")
				return
			}
		case "Offset":
			z.Offset, err = dc.ReadInt64()
			if err != nil {
				err = msgp.WrapError(err, "Offset")
				return
			}
		case "TpNanos":
			z.TpNanos, err = dc.ReadInt64()
			if err != nil {
				err = msgp.WrapError(err, "TpNanos")
				return
			}
		default:
			err = dc.Skip()
			if err != nil {
				err = msgp.WrapError(err)
				return
			}
		}
	}
	return
}

// EncodeMsg implements msgp.Encodable
func (z *LivePayload) EncodeMsg(en *msgp.Writer) (err error) {
	// map header, size 5
	// write "Message"
	err = en.Append(0x85, 0xa7, 0x4d, 0x65, 0x73, 0x73, 0x61, 0x67, 0x65)
	if err != nil {
		return
	}
	err = en.WriteBytes(z.Message)
	if err != nil {
		err = msgp.WrapError(err, "Message")
		return
	}
	// write "Topic"
	err = en.Append(0xa5, 0x54, 0x6f, 0x70, 0x69, 0x63)
	if err != nil {
		return
	}
	err = en.WriteString(z.Topic)
	if err != nil {
		err = msgp.WrapError(err, "Topic")
		return
	}
	// write "Partition"
	err = en.Append(0xa9, 0x50, 0x61, 0x72, 0x74, 0x69, 0x74, 0x69, 0x6f, 0x6e)
	if err != nil {
		return
	}
	err = en.WriteInt32(z.Partition)
	if err != nil {
		err = msgp.WrapError(err, "Partition")
		return
	}
	// write "Offset"
	err = en.Append(0xa6, 0x4f, 0x66, 0x66, 0x73, 0x65, 0x74)
	if err != nil {
		return
	}
	err = en.WriteInt64(z.Offset)
	if err != nil {
		err = msgp.WrapError(err, "Offset")
		return
	}
	// write "TpNanos"
	err = en.Append(0xa7, 0x54, 0x70, 0x4e, 0x61, 0x6e, 0x6f, 0x73)
	if err != nil {
		return
	}
	err = en.WriteInt64(z.TpNanos)
	if err != nil {
		err = msgp.WrapError(err, "TpNanos")
		return
	}
	return
}

// Msgsize returns an upper bound estimate of the number of bytes occupied by the serialized message
func (z *LivePayload) Msgsize() (s int) {
	s = 1 + 8 + msgp.BytesPrefixSize + len(z.Message) + 6 + msgp.StringPrefixSize + len(z.Topic) + 10 + msgp.Int32Size + 7 + msgp.Int64Size + 8 + msgp.Int64Size
	return
}

// DecodeMsg implements msgp.Decodable
func (z *LivePayloads) DecodeMsg(dc *msgp.Reader) (err error) {
	var field []byte
	_ = field
	var zb0001 uint32
	zb0001, err = dc.ReadMapHeader()
	if err != nil {
		err = msgp.WrapError(err)
		return
	}
	for zb0001 > 0 {
		zb0001--
		field, err = dc.ReadMapKeyPtr()
		if err != nil {
			err = msgp.WrapError(err)
			return
		}
		switch msgp.UnsafeString(field) {
		case "Payloads":
			var zb0002 uint32
			zb0002, err = dc.ReadArrayHeader()
			if err != nil {
				err = msgp.WrapError(err, "Payloads")
				return
			}
			if cap(z.Payloads) >= int(zb0002) {
				z.Payloads = (z.Payloads)[:zb0002]
			} else {
				z.Payloads = make([]LivePayload, zb0002)
			}
			for za0001 := range z.Payloads {
				err = z.Payloads[za0001].DecodeMsg(dc)
				if err != nil {
					err = msgp.WrapError(err, "Payloads", za0001)
					return
				}
			}
		case "Service":
			z.Service, err = dc.ReadString()
			if err != nil {
				err = msgp.WrapError(err, "Service")
				return
			}
		case "TracerVersion":
			z.TracerVersion, err = dc.ReadString()
			if err != nil {
				err = msgp.WrapError(err, "TracerVersion")
				return
			}
		case "TracerLang":
			z.TracerLang, err = dc.ReadString()
			if err != nil {
				err = msgp.WrapError(err, "TracerLang")
				return
			}
		default:
			err = dc.Skip()
			if err != nil {
				err = msgp.WrapError(err)
				return
			}
		}
	}
	return
}

// EncodeMsg implements msgp.Encodable
func (z *LivePayloads) EncodeMsg(en *msgp.Writer) (err error) {
	// map header, size 4
	// write "Payloads"
	err = en.Append(0x84, 0xa8, 0x50, 0x61, 0x79, 0x6c, 0x6f, 0x61, 0x64, 0x73)
	if err != nil {
		return
	}
	err = en.WriteArrayHeader(uint32(len(z.Payloads)))
	if err != nil {
		err = msgp.WrapError(err, "Payloads")
		return
	}
	for za0001 := range z.Payloads {
		err = z.Payloads[za0001].EncodeMsg(en)
		if err != nil {
			err = msgp.WrapError(err, "Payloads", za0001)
			return
		}
	}
	// write "Service"
	err = en.Append(0xa7, 0x53, 0x65, 0x72, 0x76, 0x69, 0x63, 0x65)
	if err != nil {
		return
	}
	err = en.WriteString(z.Service)
	if err != nil {
		err = msgp.WrapError(err, "Service")
		return
	}
	// write "TracerVersion"
	err = en.Append(0xad, 0x54, 0x72, 0x61, 0x63, 0x65, 0x72, 0x56, 0x65, 0x72, 0x73, 0x69, 0x6f, 0x6e)
	if err != nil {
		return
	}
	err = en.WriteString(z.TracerVersion)
	if err != nil {
		err = msgp.WrapError(err, "TracerVersion")
		return
	}
	// write "TracerLang"
	err = en.Append(0xaa, 0x54, 0x72, 0x61, 0x63, 0x65, 0x72, 0x4c, 0x61, 0x6e, 0x67)
	if err != nil {
		return
	}
	err = en.WriteString(z.TracerLang)
	if err != nil {
		err = msgp.WrapError(err, "TracerLang")
		return
	}
	return
}

// Msgsize returns an upper bound estimate of the number of bytes occupied by the serialized message
func (z *LivePayloads) Msgsize() (s int) {
	s = 1 + 9 + msgp.ArrayHeaderSize
	for za0001 := range z.Payloads {
		s += z.Payloads[za0001].Msgsize()
	}
	s += 8 + msgp.StringPrefixSize + len(z.Service) + 14 + msgp.StringPrefixSize + len(z.TracerVersion) + 11 + msgp.StringPrefixSize + len(z.TracerLang)
	return
}
