# A9 Camera for Home Assistant

Use the cheap A9 / V720 "Naxclow" mini WiFi cameras in Home Assistant, fully local, without their cloud.

<img width="826" alt="Add-on web UI" src="https://github.com/dbuezas/a9-camera-ha-add-on/assets/777196/8ea61525-02b7-40ac-8853-0c2f63285a2d">

It has two parts, both in this repository:

- **Add-on** (`a9-cam/`): pretends to be the camera cloud. The cameras connect to it, and it serves their
  video as MJPEG, JPEG snapshots and RTSP. Small Go program, no Python and no FFmpeg.
- **Integration** (`custom_components/a9_cam/`): adds each camera to Home Assistant as a device with a
  live camera entity, switches, sensors and buttons. It finds the add-on by itself.

## Which cameras work

Cameras that use the **V720** app and connect to `*.naxclow.com`. Tested with firmware `202302031142`
and `202303131805`. Reports say newer firmware (from late 2024) may not accept a local server.

"A9" is a name used for many different cameras with different chips and apps. If yours uses another app,
this project will not work with it.

## Setup

1. **Connect the camera to your WiFi** with the V720 app.
2. **Point `*.naxclow.com` to your Home Assistant IP.** For example with the AdGuard Home add-on:
   Filters → DNS rewrites → `*.naxclow.com` → your HA IP. Your router must give AdGuard as the DNS
   server, at least to the cameras. Do this after step 1: the app needs the real cloud.
3. **Install the add-on:**
   [![Add repository](https://my.home-assistant.io/badges/supervisor_add_addon_repository.svg)](https://my.home-assistant.io/redirect/supervisor_add_addon_repository/?repository_url=https%3A%2F%2Fgithub.com%2Fdbuezas%2Fa9-camera-ha-add-on)
   then install and start **A9 Camera**.
4. **Install the integration** with [HACS](https://hacs.xyz):
   [![Open in HACS](https://my.home-assistant.io/badges/hacs_repository.svg)](https://my.home-assistant.io/redirect/hacs_repository/?owner=dbuezas&repository=a9-camera-ha-add-on&category=integration)
   then restart Home Assistant.
5. Home Assistant shows **A9 Camera discovered**. Click **Configure**.
6. Restart the cameras, or wait. A camera can take up to about 10 minutes to reconnect after a restart
   of the add-on.

## What you get

For each camera, a device with:

| Entity | What it does |
| --- | --- |
| Camera | Live MJPEG video and snapshots |
| Infrared / Status LED / ... switches | Camera settings, applied and confirmed by the camera |
| Image orientation | Flip and mirror the image |
| Battery | Battery level |
| Streaming | On while somebody watches |
| Reboot | Restarts the camera |

The camera only sends video while somebody watches, so it saves battery. Dashboard pictures reuse
a recent frame instead of waking the camera every few seconds.

## go2rtc and WebRTC

The camera sends JPEG pictures, not H.264. Browsers need H.264 for WebRTC, so go2rtc must convert:

```yaml
streams:
  a9_camera: ffmpeg:rtsp://127.0.0.1:8556/CAMERA_ID#video=h264#audio=copy
```

You do not need this for normal Home Assistant camera cards. They show the MJPEG video directly,
without any conversion.

## URLs

| What | URL |
| --- | --- |
| RTSP, MJPEG video + PCMA audio | `rtsp://HA_IP:8556/CAMERA_ID` |
| MJPEG | `http://HA_IP/dev/CAMERA_ID/live` |
| JPEG snapshot | `http://HA_IP/dev/CAMERA_ID/snapshot` |

More in [the add-on docs](a9-cam/DOCS.md).

## How it works

```
camera ──DNS *.naxclow.com──▶ add-on (HTTP :80, TCP+UDP :6123) ──▶ MJPEG / RTSP / snapshots
                                         │
                                         └──▶ integration (live events + commands) ──▶ HA entities
```

The camera protocol is in [server/pkg/a9](server/pkg/a9/README.md). That package is written like a
[go2rtc](https://github.com/AlexxIT/go2rtc) source and builds inside go2rtc unchanged.

## Development

```sh
cd server
go test ./...
go run . -http :8080 -log debug
```

A release is a tag like `v3.0.0`. CI tests it and publishes the add-on images for all CPU types to
`ghcr.io/dbuezas/{arch}-a9-cam` and the binaries to the GitHub release.

## Credits

The camera protocol was reverse engineered by [intx82/a9-v720](https://github.com/intx82/a9-v720).
The stream code uses parts of [go2rtc](https://github.com/AlexxIT/go2rtc).
