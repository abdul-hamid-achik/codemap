import os
from x import y as z
CONST = 1
a, b = 1, 2
typed: int = 3
x = 1
x = 2


def top(p, q=1):
    """Doc for top."""
    local = 1

    def inner():
        pass
    return local


@decorator
class Svc(Base):
    __slots__ = ("slot_a", "slot_b")
    attr = 1
    typed_attr: str = "x"

    def __init__(self, r):
        self.r = r
        self.slot_a = r

    @property
    def prop(self):
        return 1

    @staticmethod
    def stat(v):
        v.ignored = 1

    class Inner:
        def im(self):
            pass


def f():
    pass


def f():
    pass


try:
    import fast as impl
except ImportError as exc:
    impl = None
    _ERR = exc
else:
    _ERR = None

with open("f") as fh:
    pass

for i in range(3):
    loop_var = i

if (m := 3) > 2:
    pass


def outer():
    global G
    G = 1


@overload
def ov(a: int) -> int: ...
@overload
def ov(a: str) -> str: ...
def ov(a):
    return a


_ = 5
