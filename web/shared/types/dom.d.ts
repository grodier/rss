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

// Prerendering (Chromium only): when a prerendered page was activated, from
// navigation start; 0 if it wasn't prerendered.
interface PerformanceNavigationTiming {
  readonly activationStart?: number;
}
