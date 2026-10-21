# Changelog

## 3.1.4

- Faster stream start: when the camera answers "busy" (it is still closing the previous stream),
  the add-on asks again every second instead of waiting 5 seconds.
- Frames without the JPEG end marker are dropped, so a broken frame does not show up as a grey image.
- Status LED switch: also sends code 220 (`ledEI`), which newer firmware uses instead of 210.
- Docs: also redirect `*.naxclow.com.cn`, and links for other cameras sold as "A9".

## 3.1.3

- Newer firmware (2024 and later) first asks `getDevInfo`. The add-on now gives the answer that
  works (thanks to 0x3dlux in intx82/a9-v720#51), so these cameras can go on to connect.

## 3.1.2

- Fix: with Audio off, the camera sent no video. Cameras do not start a stream in "video only" mode,
  so the stream now starts with audio and switches to video only after the first frame.

## 3.1.1

- Integration: an existing setup follows the add-on to its new port (8557) instead of showing
  "A9 Camera discovered" again. Only one setup is allowed.

## 3.1.0

- New: Audio switch per camera. Off asks the camera for video only, so no audio leaves it (#13).
- New: web UI, MJPEG, snapshots and the integration API also on port 8557. Option `camera_http_port`
  (default 80, 0 = off) for systems where another add-on uses port 80 (#1).
- Experimental: answer `getDevInfo` from newer camera firmware (#16).

## 3.0.0

- Rewrite in Go. No Python and no FFmpeg in the image anymore.
- New: RTSP on port 8556 with MJPEG video and PCMA audio, for go2rtc and the Generic Camera integration.
- One camera stream is shared by all viewers. The camera stops sending when nobody watches.
- The data channel restarts by itself when the video stops.
- New web UI with live view and buttons for IR, flip, LED and reboot.
- Option `log_level`.
- Removed: `/dev/CAMERA_ID/stream`, `/go2rtc-stream`, `/browser-stream` and `/audio`. Use the RTSP URL.

## 2.4

- Last Python version.
