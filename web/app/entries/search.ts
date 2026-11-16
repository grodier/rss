// Search's enhancements: results as you type, Follow in place, and Look
// up's busy state.
// Its stylesheet is search.css, by the group's name. The page works without
// them.
import * as busyForms from "../../shared/lib/busy-forms";
import { localizeTimes } from "../../shared/lib/local-times";
import * as backForward from "../features/back-forward";
import * as liveSearch from "../features/live-search";
import { setupRUM } from "../features/rum";
import * as saveStar from "../features/save-star";

setupRUM(); // first, so a setup that throws can't stop the page being measured
localizeTimes(document);
backForward.init();
saveStar.init();
busyForms.init(); // after saveStar: it skips the submits saveStar takes over
liveSearch.init();
