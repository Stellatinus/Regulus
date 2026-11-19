// describeUserAgent turns a raw User-Agent header into a browser, its major
// version and an operating system ("Chrome", "156", "Windows"), for the list
// of sessions (#91). It knows the common browsers only; anything else keeps
// an empty field and the page shows the raw text instead.
export interface Device {
  browser: string;
  version: string;
  os: string;
}

const BROWSERS: [RegExp, string][] = [
  [/Edg(?:e|A|iOS)?\/(\d+)/, "Edge"],
  [/OPR\/(\d+)/, "Opera"],
  [/SamsungBrowser\/(\d+)/, "Samsung Internet"],
  [/Firefox\/(\d+)/, "Firefox"],
  [/FxiOS\/(\d+)/, "Firefox"],
  [/CriOS\/(\d+)/, "Chrome"],
  [/Chrome\/(\d+)/, "Chrome"],
  [/Version\/(\d+)[^ ]* (?:Mobile\/\S+ )?Safari\//, "Safari"],
  [/curl\/(\d+)/, "curl"],
];

const SYSTEMS: [RegExp, string][] = [
  [/Windows NT/, "Windows"],
  [/iPhone|iPad|iPod/, "iOS"],
  [/Android/, "Android"],
  [/CrOS/, "ChromeOS"],
  [/Mac OS X|Macintosh/, "macOS"],
  [/Ubuntu/, "Ubuntu"],
  [/Linux/, "Linux"],
];

export function describeUserAgent(ua: string): Device {
  let browser = "";
  let version = "";
  for (const [re, name] of BROWSERS) {
    const m = re.exec(ua);
    if (m) {
      browser = name;
      version = m[1];
      break;
    }
  }
  const os = SYSTEMS.find(([re]) => re.test(ua))?.[1] ?? "";
  return { browser, version, os };
}
