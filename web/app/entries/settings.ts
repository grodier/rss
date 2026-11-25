// The pages that manage things (Settings, My feeds, tags) and have no
// enhancement of their own beyond My feeds' search, its clear button and
// dates, and a shared timeline's Copy buttons (those with password fields
// or imports are account's). Their stylesheet is settings.css, by the
// group's name. Every page works without this.
import { localizeTimes } from "../../shared/lib/local-times";
import { init as initSearchClear } from "../../shared/lib/search-clear";
import { init as initCopyField } from "../features/copy-field";
import { init as initFeedsFilter } from "../features/feeds-filter";
import { setupRUM } from "../features/rum";

setupRUM();
localizeTimes(document);
initSearchClear();
initFeedsFilter();
initCopyField();
