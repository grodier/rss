// The pages that manage things (Settings, My feeds, tags) and have no
// enhancement of their own beyond My feeds' search (those with password
// fields or imports are account's). Their stylesheet is settings.css, by the
// group's name. Every page works without this.
import { init as initFeedsFilter } from "../features/feeds-filter";
import { setupRUM } from "../features/rum";

setupRUM();
initFeedsFilter();
