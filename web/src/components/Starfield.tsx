import { useEffect, useRef } from "react";

// A slowly turning galaxy drawn on one canvas. Each star has a home on the disc;
// the cursor pulls nearby stars toward it and pushes away the ones that get too
// close, so they gather in a loose ring that follows the pointer.

interface Star {
  // home position in polar coordinates around the galaxy centre
  r: number;
  a: number;
  speed: number;
  // live position and velocity
  x: number;
  y: number;
  vx: number;
  vy: number;
  size: number;
  alpha: number;
  twinkle: number;
  warm: boolean;
}

const PULL_RADIUS = 240; // stars inside this distance drift toward the cursor
const PUSH_RADIUS = 84; // …and inside this one they are pushed back out

export function Starfield() {
  const ref = useRef<HTMLCanvasElement>(null);

  useEffect(() => {
    const canvas = ref.current;
    if (!canvas) return;
    const ctx = canvas.getContext("2d");
    if (!ctx) return;

    const still = window.matchMedia("(prefers-reduced-motion: reduce)").matches;
    let w = 0;
    let h = 0;
    let dpr = 1;
    let stars: Star[] = [];
    let raf = 0;
    let last = performance.now();
    const mouse = { x: -9999, y: -9999, active: false };

    function build() {
      dpr = Math.min(window.devicePixelRatio || 1, 2);
      w = window.innerWidth;
      h = window.innerHeight;
      canvas!.width = Math.floor(w * dpr);
      canvas!.height = Math.floor(h * dpr);
      canvas!.style.width = w + "px";
      canvas!.style.height = h + "px";
      ctx!.setTransform(dpr, 0, 0, dpr, 0, 0);

      const reach = Math.hypot(w, h) * 0.56;
      const count = Math.round(Math.min(900, Math.max(220, (w * h) / 1900)));
      stars = [];
      for (let i = 0; i < count; i++) {
        // Two thirds of the stars sit on spiral arms, the rest fill the disc.
        const onArm = Math.random() < 0.66;
        const t = Math.pow(Math.random(), 1.25);
        const r = 24 + t * reach;
        let a: number;
        if (onArm) {
          const arm = Math.floor(Math.random() * 3);
          a = (arm * Math.PI * 2) / 3 + t * 3.4 + (Math.random() - 0.5) * (0.55 - t * 0.25);
        } else {
          a = Math.random() * Math.PI * 2;
        }
        const depth = Math.random();
        const s: Star = {
          r,
          a,
          speed: (0.006 + 0.016 * (1 - t)) * (0.6 + depth * 0.8),
          x: 0,
          y: 0,
          vx: 0,
          vy: 0,
          size: 0.4 + depth * depth * 1.3,
          alpha: (0.2 + depth * 0.55) * (1 - t * 0.35),
          twinkle: Math.random() * Math.PI * 2,
          warm: Math.random() < 0.07,
        };
        const p = home(s);
        s.x = p.x;
        s.y = p.y;
        stars.push(s);
      }
    }

    // The disc is tilted, so the galaxy reads as an ellipse across the screen.
    function home(s: Star) {
      const cx = w * 0.62;
      const cy = h * 0.46;
      const px = Math.cos(s.a) * s.r;
      const py = Math.sin(s.a) * s.r * 0.52;
      const tilt = -0.34;
      return {
        x: cx + px * Math.cos(tilt) - py * Math.sin(tilt),
        y: cy + px * Math.sin(tilt) + py * Math.cos(tilt),
      };
    }

    function frame(now: number) {
      const dt = Math.min(0.05, (now - last) / 1000);
      last = now;
      ctx!.clearRect(0, 0, w, h);

      for (const s of stars) {
        s.a += s.speed * dt;
        const hp = home(s);

        // spring back toward the home position
        let ax = (hp.x - s.x) * 5.5;
        let ay = (hp.y - s.y) * 5.5;

        if (mouse.active) {
          const dx = s.x - mouse.x;
          const dy = s.y - mouse.y;
          const d = Math.hypot(dx, dy) || 0.001;
          if (d < PUSH_RADIUS) {
            const f = (1 - d / PUSH_RADIUS) * 2600;
            ax += (dx / d) * f;
            ay += (dy / d) * f;
          } else if (d < PULL_RADIUS) {
            const f = (1 - (d - PUSH_RADIUS) / (PULL_RADIUS - PUSH_RADIUS)) * 520;
            ax -= (dx / d) * f;
            ay -= (dy / d) * f;
          }
        }

        s.vx = (s.vx + ax * dt) * 0.9;
        s.vy = (s.vy + ay * dt) * 0.9;
        s.x += s.vx * dt;
        s.y += s.vy * dt;

        if (s.x < -8 || s.x > w + 8 || s.y < -8 || s.y > h + 8) continue;

        s.twinkle += dt * (0.6 + s.size * 0.5);
        const moving = Math.min(1, Math.hypot(s.vx, s.vy) / 160);
        const a = Math.min(1, s.alpha * (0.78 + 0.22 * Math.sin(s.twinkle)) + moving * 0.35);
        ctx!.globalAlpha = a;
        ctx!.fillStyle = s.warm ? "#f0b9b0" : "#e8e9f2";
        ctx!.beginPath();
        ctx!.arc(s.x, s.y, s.size, 0, Math.PI * 2);
        ctx!.fill();
      }
      ctx!.globalAlpha = 1;
      if (!still) raf = requestAnimationFrame(frame);
    }

    function onMove(e: PointerEvent) {
      mouse.x = e.clientX;
      mouse.y = e.clientY;
      mouse.active = true;
    }
    function onLeave() {
      mouse.active = false;
    }
    function onVisibility() {
      cancelAnimationFrame(raf);
      if (!document.hidden && !still) {
        last = performance.now();
        raf = requestAnimationFrame(frame);
      }
    }
    function onResize() {
      build();
      if (still) frame(performance.now());
    }

    build();
    raf = requestAnimationFrame(frame);
    window.addEventListener("pointermove", onMove, { passive: true });
    document.documentElement.addEventListener("pointerleave", onLeave);
    window.addEventListener("resize", onResize);
    document.addEventListener("visibilitychange", onVisibility);
    return () => {
      cancelAnimationFrame(raf);
      window.removeEventListener("pointermove", onMove);
      document.documentElement.removeEventListener("pointerleave", onLeave);
      window.removeEventListener("resize", onResize);
      document.removeEventListener("visibilitychange", onVisibility);
    };
  }, []);

  return <canvas ref={ref} className="starfield" aria-hidden="true" />;
}
