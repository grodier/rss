// The browser APIs TypeScript's DOM types lack, declared only as far as the
// code uses them. Each is optional where browsers may not have it.

// The Network Information API (Chromium only): the connection's effective
// type, which rum.ts reports.
interface NetworkInformation {
  readonly effectiveType?: "slow-2g" | "2g" | "3g" | "4g";
}

interface Navigator {
  readonly connection?: NetworkInformation;
}

// Why a page wasn't restored from the back/forward cache (Chrome 123 and
// later), on a back_forward navigation's entry; null when it was eligible.
interface NotRestoredReasonDetails {
  readonly reason: string;
}

interface NotRestoredReasons {
  readonly reasons: readonly NotRestoredReasonDetails[] | null;
  readonly children: readonly NotRestoredReasons[] | null;
}

// Prerendering (Chromium only): when a prerendered page was activated, from
// navigation start; 0 if it wasn't prerendered. And why a Back or Forward
// wasn't restored from the back/forward cache (Chrome only).
interface PerformanceNavigationTiming {
  readonly activationStart?: number;
  readonly notRestoredReasons?: NotRestoredReasons | null;
}
