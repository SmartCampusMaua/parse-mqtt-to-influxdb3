# parse-mqtt-to-kafka

ORGANIZATION=IMT BUCKET=SmartCampusMaua MQTT_BROKER=mqtt://mqtt.maua.br:1883 KAFKA_BROKER=localhost:9094 go run main.go

OpenDataTelemetry/IMT/LNS/SmartLight/{DeviceId}/up/imt
OpenDataTelemetry/IMT/LNS/WaterTankLevel/{DeviceId}/up/atc

```json
{
  "params": {
    "rx_time": 1756219662.9997492,
    "port": 4,
    "radio": {
      "datarate": 4,
      "modulation": {
        "bandwidth": 125000,
        "type": "LORA",
        "spreading": 8,
        "coderate": "4/5"
      },
      "hardware": {
        "status": 1,
        "snr": 4.0,
        "rssi": -105.0,
        "gps": {
          "lat": -23.646209716796875,
          "lng": -46.558780670166016,
          "alt": 852.0
        }
      },
      "time": 1756219662.9997492,
      "freq": 915.4,
      "size": 45
    },
    "counter_up": 5095,
    "payload": "/gAFUMgtPHP7AgAABA8AAAAAAIULdUkAB48DAAABbcU=",
  },
  "meta": {
    "application": "f803320100000000",
    "device_addr": "17e8d4b3",
    "time": 1756219663.066,
    "device": "f803320100030977",
    "gateway": "b0fd0b7003860000"
  },
  "type": "uplink"
}
```
