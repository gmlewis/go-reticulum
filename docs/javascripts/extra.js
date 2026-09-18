/* Dynamic facts (version, stars, forks) for stacked repository links */

(function () {
  function formatNumber(n) {
    if (typeof n !== "number") return n;
    if (n > 999) {
      var t = +((n - 950) % 1000 > 99);
      return ((n + 1e-6) / 1000).toFixed(t) + "k";
    }
    return String(n);
  }

  function renderFacts(repoEl, facts) {
    if (!facts) return;
    var repoBox = repoEl.querySelector(".md-source__repository");
    if (!repoBox || repoBox.querySelector(".md-source__facts")) return;

    var factsList = document.createElement("ul");
    factsList.className = "md-source__facts";

    if (facts.version) {
      var liVer = document.createElement("li");
      liVer.className = "md-source__fact md-source__fact--version";
      liVer.textContent = facts.version;
      factsList.appendChild(liVer);
    }

    if (typeof facts.stars === "number") {
      var liStars = document.createElement("li");
      liStars.className = "md-source__fact md-source__fact--stars";
      liStars.textContent = formatNumber(facts.stars);
      factsList.appendChild(liStars);
    }

    if (typeof facts.forks === "number") {
      var liForks = document.createElement("li");
      liForks.className = "md-source__fact md-source__fact--forks";
      liForks.textContent = formatNumber(facts.forks);
      factsList.appendChild(liForks);
    }

    if (factsList.children.length > 0) {
      repoBox.appendChild(factsList);
      repoBox.classList.add("md-source__repository--active");
    }
  }

  function initRepoFacts() {
    var repoElements = document.querySelectorAll(".md-source--custom[data-repo]");
    repoElements.forEach(function (el) {
      var repo = el.getAttribute("data-repo");
      if (!repo) return;

      var cacheKey = "__source_" + repo;
      try {
        var cached = sessionStorage.getItem(cacheKey);
        if (cached) {
          renderFacts(el, JSON.parse(cached));
          return;
        }
      } catch (e) {}

      var repoUrl = "https://api.github.com/repos/" + repo;
      var releaseUrl = repoUrl + "/releases/latest";

      Promise.all([
        fetch(releaseUrl).then(function (r) { return r.ok ? r.json() : null; }).catch(function () { return null; }),
        fetch(repoUrl).then(function (r) { return r.ok ? r.json() : null; }).catch(function () { return null; })
      ]).then(function (results) {
        var releaseData = results[0];
        var repoData = results[1];

        var facts = {};
        if (releaseData && releaseData.tag_name) {
          facts.version = releaseData.tag_name;
        }
        if (repoData) {
          if (typeof repoData.stargazers_count === "number") {
            facts.stars = repoData.stargazers_count;
          }
          if (typeof repoData.forks_count === "number") {
            facts.forks = repoData.forks_count;
          }
        }

        if (facts.version || facts.stars != null || facts.forks != null) {
          try {
            sessionStorage.setItem(cacheKey, JSON.stringify(facts));
          } catch (e) {}
          renderFacts(el, facts);
        }
      }).catch(function () {});
    });
  }

  if (typeof document$ !== "undefined") {
    document$.subscribe(initRepoFacts);
  } else if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", initRepoFacts);
  } else {
    initRepoFacts();
  }
})();
