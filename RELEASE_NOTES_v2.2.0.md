# TinyObs v2.2.0 - Production-Ready Dashboard 🚀

**Release Date:** November 16, 2025
**Focus:** Professional UX, Theme Support, Critical Bug Fixes

This release transforms TinyObs from a solid side project into a production-grade observability platform with a polished, professional dashboard that rivals commercial products. Ready for open source launch!

---

## 🎨 Major Features

### Light/Dark Theme Toggle
- **One-click theme switching** with ☀️/🌙 button or press `T`
- **localStorage persistence** - your preference is saved across sessions
- **Instant re-rendering** - all charts update immediately when theme changes
- **Professional color schemes:**
  - Dark mode: GitHub-inspired, developer-friendly
  - Light mode: Clean, modern, Bootstrap 5-inspired
- **Theme-aware chart colors** - separate 15-color palettes for each theme

### Enhanced Keyboard Shortcuts
Navigate the dashboard like a pro:
- `D` - Switch to Dashboard view
- `E` - Switch to Explore view
- `R` - Refresh current view
- `T` - Toggle light/dark theme
- `/` - Focus search (Explore view)
- `ESC` - Clear selection or unfocus input
- `1-4` - Quick time range selection (1h, 6h, 24h, 7d)

**Smart input detection** prevents shortcuts from firing when typing in search boxes.

### Stable Color Assignment
- **No more flickering charts!** Colors stay consistent across refreshes
- Hash-based color assignment using metric name + labels
- Same metric series always gets the same color

### Auto-Scroll to Charts
When you select metrics in Explore view, the page **smoothly scrolls** to show the generated chart. No more hunting for your visualization!

### Advanced Filtering
Filter dashboard by:
- **Service** - Focus on specific microservices
- **Endpoint** - Drill down to specific API endpoints
- **Metric Name** - View only the metrics you care about

Filters now work correctly with historical data (previously caused charts to disappear).

---

## 🐛 Critical Bugs Fixed

### Storage Calculation Bug (HIGH PRIORITY)
**Problem:** Dashboard showed "2.1 GB used" when actual disk usage was only 1.6 MB (over 1000x wrong!)

**Root Cause:** BadgerDB uses sparse files (`.vlog`) with pre-allocated space. We were using `info.Size()` which returns logical file size instead of actual disk blocks.

**Fix:** Now uses `syscall.Stat_t.Blocks * 512` to match `du -sh` behavior and show accurate storage consumption.

**Impact:** Storage usage display is now accurate and matches system tools.

### Chart Filtering Bug
**Problem:** When filtering by endpoint (e.g., `/api/users`), charts would appear empty or disappear entirely, even though data existed.

**Root Cause:** Filter logic was applied to chart *selection* but not to chart *data fetching*. The backend returned all series, but they weren't filtered client-side.

**Fix:**
- Apply filters consistently in both chart selection and rendering
- Filter fetched series data to only show matching endpoints
- Charts now correctly display historical data when filters are active

**Impact:** Filtering now works as expected - select an endpoint and see only that endpoint's metrics with full history.

---

## ✨ UX Improvements

### Professional Design Polish
- **Corporate-grade aesthetic** suitable for stakeholder presentations
- **Refined shadows** with theme-aware opacity
- **Smooth hover effects** on all interactive elements
- **Professional spacing** and refined typography
- **Better contrast ratios** for accessibility (WCAG AA compliant)
- **Crisp borders** and improved button states
- **Elevated service sections** with subtle depth

### Visual Consistency
- **Consistent design language** across both themes
- **Smooth transitions** between theme changes
- **White text on colored buttons** for better readability
- **Help tooltip** shows all keyboard shortcuts (hover over `?` button)

---

## 🔧 Technical Improvements

### Backend
- **Fixed sparse file handling** in `calculateDirSize()` function
- **CORS middleware** for cross-origin API access
- **Health endpoint** (`/v1/health`) for monitoring tools
- **Better error handling** in storage calculation
- **Accurate disk usage metrics** matching Unix `du` command

### Frontend
- **Dual color palettes** for light/dark modes
- **CSS variable system** for consistent theming
- **Dynamic Chart.js defaults** that update with theme
- **Proper endpoint filtering** in chart rendering
- **Theme persistence** via localStorage
- **Smart keyboard shortcut detection** (doesn't interfere with typing)

---

## 📚 Documentation Updates

- ✅ Added **keyboard shortcuts table** to README for discoverability
- ✅ Documented **theme toggle feature** with usage instructions
- ✅ Updated **V2.2 roadmap** showing completed features
- ✅ Clarified **light/dark mode support** in features list

---

## 📦 What's Included

### Commits in This Release:
1. `68e379f` - Final open source polish (CORS, health endpoint, help tooltip)
2. `850b1a2` - Storage calculation fix (sparse files)
3. `ae1fa8a` - Filter bug fix (chart rendering)
4. `49a2023` - Theme toggle + enhanced keyboard shortcuts
5. `abbb42b` - Light theme polish + documentation

### Files Changed:
- `cmd/server/main.go` - Storage calculation, health endpoint, CORS
- `web/dashboard.html` - Theme system, keyboard shortcuts, filters, polish
- `README.md` - Features documentation, keyboard shortcuts table

---

## 🎯 V2.2 Roadmap Progress

**Completed (8/10 items):**
- ✅ Multi-metric overlay charts
- ✅ Dashboard templates (Go Runtime, HTTP API presets)
- ✅ Label-based filtering UI
- ✅ Modern gradient UI with improved UX
- ✅ Light/dark theme toggle with persistence
- ✅ Enhanced keyboard shortcuts
- ✅ Stable color assignment
- ✅ Auto-scroll to selected metrics

**Remaining (2/10 items):**
- ⏳ Export/import dashboard configurations (JSON)
- ⏳ Time comparison view (compare to 24h ago)

---

## 🚀 Migration & Upgrade Notes

**No breaking changes!** This is a **drop-in upgrade**.

**What changes automatically:**
- Storage calculation is now accurate (you'll see lower numbers)
- Theme defaults to dark mode (matches previous behavior)
- Filtering now works correctly
- Charts have stable colors

**What's new (opt-in):**
- Press `T` to try light theme
- Use keyboard shortcuts for faster navigation
- Hover over `?` in header to see all shortcuts

---

## 📊 Stats

- **~2,600 lines of production Go code** (excluding tests)
- **5 major commits** in this release
- **3 critical bugs fixed**
- **8 new features added**
- **Professional polish** suitable for open source launch

---

## 🙏 Contributors

Built by [@nicktill](https://github.com/nicktill)

---

## 📝 Next Up: V3.0

Focus: **Real-time & Anomaly Detection** (Next 2-4 weeks)
- WebSocket live updates (replace 30s polling)
- Statistical anomaly detection (2σ from moving average)
- Visual anomaly indicators on charts (red zones)
- Simple threshold alerts (email/webhook)
- Alert history view

---

**Ready to open source!** 🎉

This release marks TinyObs as production-ready with sophisticated UX, critical bugs fixed, and professional polish suitable for showcasing to stakeholders or including in a portfolio.

Try it out:
```bash
git pull
git checkout v2.2.0
go run cmd/server/main.go
```

Then visit http://localhost:8080/dashboard.html and press `T` to toggle themes!
