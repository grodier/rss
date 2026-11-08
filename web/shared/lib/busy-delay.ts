// busyDelay is --delay-busy (tokens.css) in ms: how long a request runs
// before it shows it's busy, so a quick one never flickers. Read once.
let delay: number | undefined;

export function busyDelay(): number {
  delay ??= parseFloat(getComputedStyle(document.documentElement).getPropertyValue("--delay-busy")) || 300;
  return delay;
}
