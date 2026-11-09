// Search's and a lookup's enhancements. Their stylesheet is search.css, by
// the group's name. Every page works without them.
import * as busyForms from "../../shared/lib/busy-forms";
import * as backForward from "../features/back-forward";
import * as liveSearch from "../features/live-search";
import * as lookupStatus from "../features/lookup-status";
import { setupRUM } from "../features/rum";

setupRUM(); // first, so a setup that throws can't stop the page being measured
backForward.init();
busyForms.init();
liveSearch.init();
lookupStatus.init();
