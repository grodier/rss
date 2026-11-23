// Discover's and a lookup's enhancements. Their stylesheet is discover.css, by
// the group's name. Every page works without them.
import * as busyForms from "../../shared/lib/busy-forms";
import { localizeTimes } from "../../shared/lib/local-times";
import * as searchClear from "../../shared/lib/search-clear";
import * as backForward from "../features/back-forward";
import * as liveSearch from "../features/live-search";
import * as lookupStatus from "../features/lookup-status";
import * as recentSearches from "../features/recent-searches";
import * as saveStar from "../features/save-star";
import { setupRUM } from "../features/rum";

setupRUM(); // first, so a setup that throws can't stop the page being measured
localizeTimes(document);
backForward.init();
saveStar.init();
busyForms.init();
searchClear.init();
liveSearch.init();
recentSearches.init();
lookupStatus.init();
