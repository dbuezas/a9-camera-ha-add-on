# This add-on moved

**New home: https://github.com/dbuezas/a9-camera-ha-add-on**

This entry in `dbuezas/ha-addons` gets no more updates. Please switch:

1. Note your camera IDs in the add-on web UI.
2. Settings → Add-ons → Add-on store → ⋮ → Repositories → add
   `https://github.com/dbuezas/a9-camera-ha-add-on`.
3. Uninstall this add-on, then install **A9 Camera** from the new repository and start it.
4. Install the **A9 Camera** integration from HACS (same repository). It finds the add-on by itself and
   adds each camera as a device with live video, switches and sensors.

Version 3 is a rewrite: no Python, no FFmpeg, much more reliable.

If you used go2rtc with `/dev/CAMERA_ID/go2rtc-stream`, that URL is gone. Use:

```yaml
streams:
  a9_camera: ffmpeg:rtsp://127.0.0.1:8556/CAMERA_ID#video=h264#audio=copy
```

Full documentation: https://github.com/dbuezas/a9-camera-ha-add-on/blob/main/a9-cam/DOCS.md
