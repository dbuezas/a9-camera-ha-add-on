# a9 add on

This addon https://github.com/dbuezas/a9-camera-ha-add-on

Python code derived from https://github.com/intx82/a9-v720/ with these changes:

- Added endpoint for a combined audio+video stream via ffmpeg
- Removed all features not strictly required for streaming video
- Removed all dependencies not needed for streaming video (particularly open-cv, which doesn't run in alpine)

All credit for reverse engineer this camera's protocol: https://github.com/intx82/a9-v720/
