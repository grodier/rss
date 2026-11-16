// Search's enhancements: results as you type, and Look up's busy state.
// Its stylesheet is search.css, by the group's name. The page works without
// them.
import * as busyForms from "../../shared/lib/busy-forms";
import { localizeTimes } from "../../shared/lib/local-times";
import * as backForward from "../features/back-forward";
import * as liveSearch from "../features/live-search";
import { setupRUM } from "../features/rum";

setupRUM(); // first, so a setup that throws can't stop the page being measured
localizeTimes(document);
backForward.init();
busyForms.init();
liveSearch.init();
