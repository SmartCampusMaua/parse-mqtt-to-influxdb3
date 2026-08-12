package milesight

import (
	"time"

	"github.com/OpenDataTelemetry/device-gateway-mqtt/go-parse/record"
)

// decodeVS373 supports both firmware generations: v1.0.1 (channels 0x03-0x05)
// and v1.0.2 (channels 0x07, 0x09-0x0C), unified onto the same sensor_type set.
func decodeVS373(entries []TLV, deviceID, provider string, ts time.Time) []record.SensorDataRecord {
	const dm = "vs373"
	st := record.ST
	var out []record.SensorDataRecord

	regionOccupancy := [6]string{
		st.Region1Occupancy, st.Region2Occupancy, st.Region3Occupancy,
		st.Region4Occupancy, st.Region5Occupancy, st.Region6Occupancy,
	}
	regionOutOfBedTime := [6]string{
		st.Region1OutOfBedTime, st.Region2OutOfBedTime, st.Region3OutOfBedTime,
		st.Region4OutOfBedTime, st.Region5OutOfBedTime, st.Region6OutOfBedTime,
	}

	for _, e := range entries {
		switch {
		// detection target (v1.0.1): status(1B)+target(1B)+use_time_now(2B)+use_time_today(2B)
		case e.Channel == 0x03 && e.Type == 0xF8:
			out = append(out, record.NewInt(st.DetectionStatus, dm, deviceID, provider, int64(e.Data[0]), ts))
			out = append(out, record.NewInt(st.TargetStatus, dm, deviceID, provider, int64(e.Data[1]), ts))
			out = append(out, record.NewInt(st.UseTimeNow, dm, deviceID, provider, int64(le16(e.Data[2:4])), ts))
			out = append(out, record.NewInt(st.UseTimeToday, dm, deviceID, provider, int64(le16(e.Data[4:6])), ts))

		// detection target (v1.0.2): status(1B)+target(1B)+use_time_now(3B)+use_time_today(3B)
		case e.Channel == 0x07 && e.Type == 0xB0:
			out = append(out, record.NewInt(st.DetectionStatus, dm, deviceID, provider, int64(e.Data[0]), ts))
			out = append(out, record.NewInt(st.TargetStatus, dm, deviceID, provider, int64(e.Data[1]), ts))
			out = append(out, record.NewInt(st.UseTimeNow, dm, deviceID, provider, int64(le24(e.Data[2:5])), ts))
			out = append(out, record.NewInt(st.UseTimeToday, dm, deviceID, provider, int64(le24(e.Data[5:8])), ts))

		// region occupancy (v1.0.1): 4 regions, 1B each. Raw encoding is inverted
		// relative to v1.0.2: 0=occupied, 1=vacant.
		case e.Channel == 0x04 && e.Type == 0xF9:
			for j := 0; j < 4; j++ {
				out = append(out, record.NewBool(regionOccupancy[j], dm, deviceID, provider, e.Data[j] == 0x00, ts))
			}

		// region occupancy (v1.0.2): region_count(1B)+bitmask(4B LE), bit=1 -> occupied.
		case e.Channel == 0x0A && e.Type == 0xB3:
			count := int(e.Data[0])
			if count > 6 {
				count = 6
			}
			mask := le32(e.Data[1:5])
			for j := 0; j < count; j++ {
				out = append(out, record.NewBool(regionOccupancy[j], dm, deviceID, provider, (mask>>uint(j))&0x01 == 1, ts))
			}

		// out-of-bed time (v1.0.1): 4 regions, 2B each.
		case e.Channel == 0x05 && e.Type == 0xFA:
			for j := 0; j < 4; j++ {
				out = append(out, record.NewInt(regionOutOfBedTime[j], dm, deviceID, provider, int64(le16(e.Data[j*2:j*2+2])), ts))
			}

		// out-of-bed time (v1.0.2), regions 1-3: 3B each.
		case e.Channel == 0x0B && e.Type == 0xB4:
			for j := 0; j < 3; j++ {
				out = append(out, record.NewInt(regionOutOfBedTime[j], dm, deviceID, provider, int64(le24(e.Data[j*3:j*3+3])), ts))
			}

		// out-of-bed time (v1.0.2), regions 4-6: 3B each.
		case e.Channel == 0x0C && e.Type == 0xB4:
			for j := 0; j < 3; j++ {
				out = append(out, record.NewInt(regionOutOfBedTime[3+j], dm, deviceID, provider, int64(le24(e.Data[j*3:j*3+3])), ts))
			}

		// breathing detection: respiratory_status(1B)+respiratory_rate(2B, ÷100)
		case e.Channel == 0x08 && e.Type == 0xB1:
			out = append(out, record.NewInt(st.RespiratoryStatus, dm, deviceID, provider, int64(e.Data[0]), ts))
			out = append(out, record.NewFloat(st.RespiratoryRate, dm, deviceID, provider, float64(le16(e.Data[1:3]))/100.0, ts))

		// alarm event: alarm_id(2B)+alarm_type(1B)+alarm_status(1B)+region_id(1B, only
		// meaningful for out_of_bed(3)/bradynea(6)/tachypnea(7) alarm types)
		case e.Channel == 0x06 && e.Type == 0xFB:
			alarmType := e.Data[2]
			out = append(out, record.NewInt(st.AlarmID, dm, deviceID, provider, int64(le16(e.Data[0:2])), ts))
			out = append(out, record.NewInt(st.AlarmType, dm, deviceID, provider, int64(alarmType), ts))
			out = append(out, record.NewInt(st.AlarmStatus, dm, deviceID, provider, int64(e.Data[3]), ts))
			if alarmType == 3 || alarmType == 6 || alarmType == 7 {
				out = append(out, record.NewInt(st.AlarmRegionID, dm, deviceID, provider, int64(e.Data[4]), ts))
			}
		}
	}
	return out
}

// DecodeVS373 decodes VS373 binary payloads from LoRaWAN uplinks.
func DecodeVS373(payload []byte, deviceID, provider string, ts time.Time) []record.SensorDataRecord {
	return decodeVS373(ParseMilesightTLV(payload), deviceID, provider, ts)
}
