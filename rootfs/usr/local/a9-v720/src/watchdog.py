import threading

class Watchdog:
    def __init__(self, timeout, callback):
        self._timeout = timeout
        self._callback = callback
        self._timer = None
        self._enabled = False

    @property
    def enabled(self):
        return self._enabled

    def start(self):
        self._enabled = True
        self.reset()

    def stop(self):
        self._enabled = False
        if self._timer is not None:
            self._timer.cancel()

    def reset(self):
        if self._timer is not None:
            self._timer.cancel()

        if self._enabled:
            self._timer = threading.Timer(self._timeout, self._timeout_occurred)
            self._timer.start()

    def _timeout_occurred(self):
        if self._enabled:
            self._callback()
