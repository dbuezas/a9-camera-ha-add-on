# A9 Camera fake server

This add-on pretends to be the cloud server of the cheap A9 / V720 "Naxclow" mini cameras.
The cameras connect to it instead of China, and it serves their video and audio on your network.

## Setup

1. Connect the camera to your WiFi (with the V720 app, or see https://github.com/intx82/a9-v720).
2. Point `*.naxclow.com` to the IP of your Home Assistant machine.
   With the AdGuard add-on: Filters → DNS rewrites → `*.naxclow.com` → your HA IP.
   Your router must give AdGuard as the DNS server to the cameras.
3. Install and start this add-on.
4. Restart the cameras, or wait. After a server restart a camera can take up to about 10 minutes to
   come back.
5. Open the add-on web UI. Each connected camera shows up with its ID and URLs.
6. Optional but recommended: install the **A9 Camera** integration from HACS (same repository) and
   restart Home Assistant. It finds this add-on by itself and adds each camera as a device with a live
   camera, switches and sensors.

## Streams

Replace `CAMERA_ID` with the ID shown in the web UI (for example `0800c0035826`).

| What | URL |
| --- | --- |
| RTSP, MJPEG video + PCMA audio | `rtsp://HA_IP:8556/CAMERA_ID` |
| MJPEG over HTTP | `http://HA_IP/dev/CAMERA_ID/live` |
| JPEG snapshot | `http://HA_IP/dev/CAMERA_ID/snapshot` (add `?max_age=60` to reuse a recent frame) |
| Camera list (JSON) | `http://HA_IP/dev/list` |

The camera only sends video while someone watches. All viewers of one camera share one stream.

### go2rtc / WebRTC card

Browsers need H.264 for WebRTC, so let go2rtc convert the video:

```yaml
streams:
  a9_camera: ffmpeg:rtsp://127.0.0.1:8556/CAMERA_ID#video=h264#audio=copy
```

### Generic Camera integration

- Stream source: `rtsp://127.0.0.1:8556/CAMERA_ID`
- Still image: `http://127.0.0.1/dev/CAMERA_ID/snapshot`

## Commands

`http://HA_IP/dev/CAMERA_ID/cmd?code=...` sends a command to the camera. The web UI has buttons for these:

| Command | URL query |
| --- | --- |
| Infrared on / off | `code=202&IrLed=1` / `code=202&IrLed=0` |
| Flip image | `code=216&mirrorFlip=4` / `code=216&mirrorFlip=0` |
| Status LED on / off | `code=210&instLed=1` / `code=210&instLed=0` |
| Reboot | `code=299&reboot=1` |

## Upgrading from version 2.x

Version 3 is a rewrite in Go. It does not use Python or FFmpeg anymore.

- `/dev/CAMERA_ID/live`, `/snapshot`, `/cmd` and `/dev/list` work as before.
- `/dev/CAMERA_ID/go2rtc-stream`, `/stream`, `/browser-stream` and `/audio` are gone (they needed FFmpeg).
  Use the RTSP URL instead, see the go2rtc example above.

## Ports

The add-on uses the host network: port 80 (the cameras call it, it cannot change), 6123 TCP and UDP
(camera connection) and 8556 (RTSP).

## Credits

Camera protocol reverse engineered by https://github.com/intx82/a9-v720.
The stream code uses parts of [go2rtc](https://github.com/AlexxIT/go2rtc).
