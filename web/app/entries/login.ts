// Log in's, Sign up's, Reset password's and the landing page's (its
// waitlist form) enhancements. Their stylesheet is solo.css, which the other
// logged-out pages share. Every page works without them. These pages are
// only for someone logged out, whatever the reason (logging out lands on Log
// in), so they also forget Discover's recent searches, for shared devices;
// solo's pages aren't, as a logged-in reader sees its error pages too.
import "../styles/solo.css";
import * as busyForms from "../../shared/lib/busy-forms";
import * as passwordToggle from "../../shared/lib/password-toggle";
import * as backForward from "../features/back-forward";
import { clearRecentSearches } from "../features/recent-searches";
import { setupRUM } from "../features/rum";

setupRUM(); // first, so a setup that throws can't stop the page being measured
backForward.init();
busyForms.init();
passwordToggle.init();
clearRecentSearches();
