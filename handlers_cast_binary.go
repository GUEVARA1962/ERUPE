package channelserver

import (
	"fmt"
	"math"
	"strings"

	"github.com/Andoryuuta/byteframe"
	"github.com/Solenataris/Erupe/network/binpacket"
	"github.com/Solenataris/Erupe/network/mhfpacket"
)

// MSG_SYS_CAST[ED]_BINARY types enum
const (
	BinaryMessageTypeState      = 0
	BinaryMessageTypeChat       = 1
	BinaryMessageTypeMailNotify = 4
	BinaryMessageTypeEmote      = 6
)

// MSG_SYS_CAST[ED]_BINARY broadcast types enum
const (
	BroadcastTypeTargeted = 0x01
	BroadcastTypeStage    = 0x03
	BroadcastTypeRavi     = 0x06
	BroadcastTypeWorld    = 0x0a
)

func sendServerChatMessage(s *Session, message string) {
	// Make the inside of the casted binary
	bf := byteframe.NewByteFrame()
	bf.SetLE()
	msgBinChat := &binpacket.MsgBinChat{
		Unk0:       0,
		Type:       5,
		Flags:      0x80,
		Message:    message,
		SenderName: "Erupe",
	}
	msgBinChat.Build(bf)

	castedBin := &mhfpacket.MsgSysCastedBinary{
		CharID:         s.charID,
		MessageType:    BinaryMessageTypeChat,
		RawDataPayload: bf.Data(),
	}

	s.QueueSendMHF(castedBin)
}

func handleMsgSysCastBinary(s *Session, p mhfpacket.MHFPacket) {
	pkt := p.(*mhfpacket.MsgSysCastBinary)

	if pkt.BroadcastType == 0x03 && pkt.MessageType == 0x03 && len(pkt.RawDataPayload) == 0x10 {
		tmp := byteframe.NewByteFrameFromBytes(pkt.RawDataPayload)
		if tmp.ReadUint16() == 0x0002 && tmp.ReadUint8() == 0x18 {
			_ = tmp.ReadBytes(9)
			tmp.SetLE()
			frame := tmp.ReadUint32()
			sendServerChatMessage(s, fmt.Sprintf("TIME : %d'%d.%03d (%dframe)", frame/30/60, frame/30%60, int(math.Round(float64(frame%30*100)/3)), frame))
		}
	}

	// Parse out the real casted binary payload
	var realPayload []byte
	var msgBinTargeted *binpacket.MsgBinTargeted
	if pkt.BroadcastType == BroadcastTypeTargeted {
		bf := byteframe.NewByteFrameFromBytes(pkt.RawDataPayload)
		msgBinTargeted = &binpacket.MsgBinTargeted{}
		err := msgBinTargeted.Parse(bf)

		if err != nil {
			s.logger.Warn("Failed to parse targeted cast binary")
			return
		}

		realPayload = msgBinTargeted.RawDataPayload
	} else {
		realPayload = pkt.RawDataPayload
	}

	// Make the response to forward to the other client(s).
	resp := &mhfpacket.MsgSysCastedBinary{
		CharID:         s.charID,
		BroadcastType:  pkt.BroadcastType, // (The client never uses Type0 upon receiving)
		MessageType:    pkt.MessageType,
		RawDataPayload: realPayload,
	}

	// Send to the proper recipients.
	switch pkt.BroadcastType {
	case BroadcastTypeWorld:
		s.server.BroadcastMHF(resp, s)
	case BroadcastTypeStage:
		s.stage.BroadcastMHF(resp, s)
	case BroadcastTypeRavi:
		if pkt.MessageType == 1 {
			session := s.server.semaphore["hs_l0u3B51J9k3"]
			(*session).BroadcastMHF(resp, s)
		}
	case BroadcastTypeTargeted:
		for _, targetID := range (*msgBinTargeted).TargetCharIDs {
			char := s.server.FindSessionByCharID(targetID)

			if char != nil {
				char.QueueSendMHF(resp)
			}
		}
	default:
		s.Lock()
		haveStage := s.stage != nil
		if haveStage {
			s.stage.BroadcastMHF(resp, s)
		}
		s.Unlock()
	}

	// Handle chat
	if pkt.MessageType == BinaryMessageTypeChat {
		bf := byteframe.NewByteFrameFromBytes(realPayload)

		// IMPORTANT! Casted binary objects are sent _as they are in memory_,
		// this means little endian for LE CPUs, might be different for PS3/PS4/PSP/XBOX.
		bf.SetLE()

		chatMessage := &binpacket.MsgBinChat{}
		chatMessage.Parse(bf)

		fmt.Printf("Got chat message: %+v\n", chatMessage)

		// Discord integration
		if s.server.erupeConfig.Discord.Enabled {
			message := fmt.Sprintf("%s: %s", chatMessage.SenderName, chatMessage.Message)
			s.server.discordSession.ChannelMessageSend(s.server.erupeConfig.Discord.ChannelID, message)
		}

		// RAVI COMMANDS
		if _, exists := s.server.semaphore["hs_l0u3B51J9k3"]; exists {
			s.server.semaphoreLock.Lock()
			getSemaphore := s.server.semaphore["hs_l0u3B51J9k3"]
			s.server.semaphoreLock.Unlock()
			if _, exists := getSemaphore.reservedClientSlots[s.charID]; exists {
				if strings.HasPrefix(chatMessage.Message, "!ravistart") {
					row := s.server.db.QueryRow("SELECT raviposttime, ravistarted FROM raviregister WHERE refid = 12")
					var raviPosted, raviStarted uint32
					err := row.Scan(&raviPosted, &raviStarted)
					if err != nil {
						panic(err)
						return
					}
					if raviStarted == 0 {
						sendServerChatMessage(s, fmt.Sprintf("Raviente will start in less than 10 seconds"))
						s.server.db.Exec("UPDATE raviregister SET ravistarted = $1", raviPosted)
					} else {
						sendServerChatMessage(s, fmt.Sprintf("Raviente has already started"))
					}
				}
				if strings.HasPrefix(chatMessage.Message, "!bressend") {
					row := s.server.db.QueryRow("SELECT unknown20 FROM ravistate WHERE refid = 29")
					var berserkRes uint32
					err := row.Scan(&berserkRes)
					if err != nil {
						panic(err)
						return
					}
					if berserkRes > 0 {
						sendServerChatMessage(s, fmt.Sprintf("Sending ressurection support"))
						s.server.db.Exec("UPDATE ravistate SET unknown20 = $1", 0)
					} else {
						sendServerChatMessage(s, fmt.Sprintf("Ressurection support has not been requested"))
					}
				}
				if strings.HasPrefix(chatMessage.Message, "!bsedsend") {
					hprow := s.server.db.QueryRow("SELECT phase1hp, phase2hp, phase3hp, phase4hp, phase5hp FROM ravistate WHERE refid = 29")
					var phase1HP, phase2HP, phase3HP, phase4HP, phase5HP uint32
					hperr := hprow.Scan(&phase1HP, &phase2HP, &phase3HP, &phase4HP, &phase5HP)
					if hperr != nil {
						panic(hperr)
						return
					}
					row := s.server.db.QueryRow("SELECT support2 FROM ravisupport WHERE refid = 25")
					var berserkTranq uint32
					err := row.Scan(&berserkTranq)
					if err != nil {
						panic(err)
						return
					}
					sendServerChatMessage(s, fmt.Sprintf("Sending sedation support if requested"))
					s.server.db.Exec("UPDATE ravisupport SET support2 = $1", (phase1HP + phase2HP + phase3HP + phase4HP + phase5HP))
				}
				if strings.HasPrefix(chatMessage.Message, "!bsedreq") {
					hprow := s.server.db.QueryRow("SELECT phase1hp, phase2hp, phase3hp, phase4hp, phase5hp FROM ravistate WHERE refid = 29")
					var phase1HP, phase2HP, phase3HP, phase4HP, phase5HP uint32
					hperr := hprow.Scan(&phase1HP, &phase2HP, &phase3HP, &phase4HP, &phase5HP)
					if hperr != nil {
						panic(hperr)
						return
					}
					row := s.server.db.QueryRow("SELECT support2 FROM ravisupport WHERE refid = 25")
					var berserkTranq uint32
					err := row.Scan(&berserkTranq)
					if err != nil {
						panic(err)
						return
					}
					sendServerChatMessage(s, fmt.Sprintf("Requesting sedation support"))
					s.server.db.Exec("UPDATE ravisupport SET support2 = $1", ((phase1HP + phase2HP + phase3HP + phase4HP + phase5HP) + 12))
				}
				if strings.HasPrefix(chatMessage.Message, "!setmultiplier ") {
					var num uint8
					n, numerr := fmt.Sscanf(chatMessage.Message, "!setmultiplier %d", &num)
					row := s.server.db.QueryRow("SELECT damagemultiplier FROM ravistate WHERE refid = 29")
					var damageMultiplier uint32
					err := row.Scan(&damageMultiplier)
					if err != nil {
						panic(err)
						return
					}
					if numerr != nil || n != 1 {
						sendServerChatMessage(s, fmt.Sprintf("Please use the format !setmultiplier x"))
					} else if damageMultiplier == 1 {
						if num > 20 {
							sendServerChatMessage(s, fmt.Sprintf("Max multiplier for Ravi is 20, setting to this value"))
							s.server.db.Exec("UPDATE ravistate SET damagemultiplier = $1", 20)
						} else {
							sendServerChatMessage(s, fmt.Sprintf("Setting Ravi damage multiplier to %d", num))
							s.server.db.Exec("UPDATE ravistate SET damagemultiplier = $1", num)
						}
					} else {
						sendServerChatMessage(s, fmt.Sprintf("Multiplier can only be set once, please restart Ravi to set again"))
					}
				}
				if strings.HasPrefix(chatMessage.Message, "!checkmultiplier") {
					var damageMultiplier uint32
					row := s.server.db.QueryRow("SELECT damagemultiplier FROM ravistate WHERE refid = 29").Scan(&damageMultiplier)
					if row != nil {
						return
					}
					sendServerChatMessage(s, fmt.Sprintf("Ravi's current damage multiplier is %d", damageMultiplier))
				}

				if strings.HasPrefix(chatMessage.Message, "!ravich") {
					row := s.server.db.QueryRow("SELECT ravitype FROM raviregister WHERE refid = 12")
					var ravitype uint32
					err := row.Scan(&ravitype)
					if err != nil {
						panic(err)
						return
					}
					if ravitype >= 0 {
						sendServerChatMessage(s, fmt.Sprintf("Raviente Type changed"))
						s.server.db.Exec("UPDATE raviregister SET ravitype = $1", 4)
					} else {
						sendServerChatMessage(s, fmt.Sprintf("Raviente Type already changed"))
					}
				}
			}
		}
		// END OF RAVI COMMANDS

		//狩コON
		if strings.HasPrefix(chatMessage.Message, "!preon") {
			row := s.server.db.QueryRow("SELECT netcafe_points FROM characters WHERE id = $1", s.charID)
			var netcafe_points int
			err := row.Scan(&netcafe_points)
			if err != nil {
				panic(err)
				return
			}
			if netcafe_points >= 150000 {
				row := s.server.db.QueryRow("SELECT rights FROM users INNER JOIN characters ON users.id = characters.user_id WHERE characters.id = $1", int(s.charID))
				var rights uint32
				err := row.Scan(&rights)
				if err != nil {
					panic(err)
					return
				}
				if rights == 14 {
					sendServerChatMessage(s, fmt.Sprintf("Premium course applied"))
					s.server.db.Exec("UPDATE users SET rights = $1 FROM characters WHERE users.id = characters.user_id AND characters.id = $2", 1073749838, int(s.charID))
					s.server.db.Exec("UPDATE characters SET netcafe_points=netcafe_points::int - 150000 WHERE id=$1", s.charID)
				} else {
					sendServerChatMessage(s, fmt.Sprintf("Premium courses have already been applied"))
				}
			} else {
				sendServerChatMessage(s, fmt.Sprintf("Points are missing"))
			}

		}

		//狩コOFF
		if strings.HasPrefix(chatMessage.Message, "!preoff") {
			row := s.server.db.QueryRow("SELECT rights FROM users INNER JOIN characters ON users.id = characters.user_id WHERE characters.id = $1", int(s.charID))
			var rights uint32
			err := row.Scan(&rights)
			if err != nil {
				panic(err)
				return
			}
			if rights == 1073749838 {
				sendServerChatMessage(s, fmt.Sprintf("Normal course applied"))
				s.server.db.Exec("UPDATE users SET rights = $1 FROM characters WHERE users.id = characters.user_id AND characters.id = $2", 14, int(s.charID))
				s.server.db.Exec("UPDATE characters SET netcafe_points=netcafe_points::int + 133000 WHERE id=$1", s.charID)
			} else {
				sendServerChatMessage(s, fmt.Sprintf("Normal courses have already been applied"))
			}

		}

		if strings.HasPrefix(chatMessage.Message, "!tele ") {
			var x, y int16
			n, err := fmt.Sscanf(chatMessage.Message, "!tele %d %d", &x, &y)
			if err != nil || n != 2 {
				sendServerChatMessage(s, "Invalid command. Usage:\"!tele 500 500\"")
			} else {
				sendServerChatMessage(s, fmt.Sprintf("Teleporting to %d %d", x, y))

				// Make the inside of the casted binary
				payload := byteframe.NewByteFrame()
				payload.SetLE()
				payload.WriteUint8(2) // SetState type(position == 2)
				payload.WriteInt16(x) // X
				payload.WriteInt16(y) // Y
				payloadBytes := payload.Data()

				s.QueueSendMHF(&mhfpacket.MsgSysCastedBinary{
					CharID:         s.charID,
					MessageType:    BinaryMessageTypeState,
					RawDataPayload: payloadBytes,
				})
			}
		}
	}
}

func handleMsgSysCastedBinary(s *Session, p mhfpacket.MHFPacket) {}
