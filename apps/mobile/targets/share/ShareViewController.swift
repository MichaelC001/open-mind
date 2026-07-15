import UIKit
import UniformTypeIdentifiers

// Inline share UI for Openmind: receives a URL or text from the share sheet,
// POSTs it to {instanceUrl}/api/items with the Bearer token, and dismisses.
// Credentials come from the App Group UserDefaults suite, mirrored there by
// the main app's settings screen (lib/settings.ts via ExtensionStorage).
//
// A plain UIViewController is used instead of SLComposeServiceViewController —
// the compose base class eagerly builds its own UI underneath custom views and
// wastes the extension's ~120 MB memory budget.
class ShareViewController: UIViewController {

  private let appGroup = "group.fun.gilla.openmind"

  // Warm palette from packages/ui design tokens.
  private let paper = UIColor(red: 0.957, green: 0.941, blue: 0.902, alpha: 1) // #F4F0E6
  private let ink = UIColor(red: 0.110, green: 0.102, blue: 0.086, alpha: 1) // #1C1A16
  private let inkMuted = UIColor(red: 0.341, green: 0.325, blue: 0.290, alpha: 1) // #57534A
  private let green = UIColor(red: 0.180, green: 0.490, blue: 0.357, alpha: 1) // #2E7D5B
  private let terracotta = UIColor(red: 0.761, green: 0.290, blue: 0.180, alpha: 1) // #C24A2E

  private let card = UIView()
  private let titleLabel = UILabel()
  private let detailLabel = UILabel()
  private let spinner = UIActivityIndicatorView(style: .medium)

  override func viewDidLoad() {
    super.viewDidLoad()
    view.backgroundColor = UIColor.black.withAlphaComponent(0.25)
    buildCard()
    setState(title: "Saving to Openmind…", detail: nil, busy: true)
  }

  override func viewDidAppear(_ animated: Bool) {
    super.viewDidAppear(animated)
    loadSharedContent { [weak self] url, text in
      DispatchQueue.main.async { self?.save(url: url, text: text) }
    }
  }

  // MARK: - Content loading

  /// Walks every attachment on every input item (shares can carry mixed
  /// content) and hands back the first URL and/or plain-text payload found.
  private func loadSharedContent(completion: @escaping (URL?, String?) -> Void) {
    let providers = (extensionContext?.inputItems as? [NSExtensionItem])?
      .compactMap(\.attachments).flatMap { $0 } ?? []

    guard !providers.isEmpty else {
      completion(nil, nil)
      return
    }

    var foundURL: URL?
    var foundText: String?
    let group = DispatchGroup()

    for provider in providers {
      if provider.hasItemConformingToTypeIdentifier(UTType.url.identifier) {
        group.enter()
        provider.loadItem(forTypeIdentifier: UTType.url.identifier, options: nil) { item, _ in
          if let url = item as? URL, foundURL == nil { foundURL = url }
          group.leave()
        }
      } else if provider.hasItemConformingToTypeIdentifier(UTType.plainText.identifier) {
        group.enter()
        provider.loadItem(forTypeIdentifier: UTType.plainText.identifier, options: nil) { item, _ in
          if let text = item as? String, foundText == nil { foundText = text }
          group.leave()
        }
      }
    }

    group.notify(queue: .main) { completion(foundURL, foundText) }
  }

  // MARK: - Save

  private func save(url: URL?, text: String?) {
    let defaults = UserDefaults(suiteName: appGroup)
    guard
      let instanceUrl = defaults?.string(forKey: "instanceUrl"), !instanceUrl.isEmpty,
      let token = defaults?.string(forKey: "token"), !token.isEmpty
    else {
      finish(success: false, message: "Open Openmind and connect to your instance first.")
      return
    }

    // Shared text that is itself a bare link is treated as a URL save.
    var body: [String: String] = [:]
    if let url {
      body["url"] = url.absoluteString
    } else if let text = text?.trimmingCharacters(in: .whitespacesAndNewlines), !text.isEmpty {
      if let asURL = URL(string: text), let scheme = asURL.scheme,
         ["http", "https"].contains(scheme.lowercased()) {
        body["url"] = text
      } else {
        body["note"] = text
      }
    } else {
      finish(success: false, message: "Nothing shareable found.")
      return
    }

    guard let endpoint = URL(string: "\(instanceUrl)/api/items"),
          let payload = try? JSONSerialization.data(withJSONObject: body)
    else {
      finish(success: false, message: "Invalid instance URL.")
      return
    }

    var request = URLRequest(url: endpoint)
    request.httpMethod = "POST"
    request.httpBody = payload
    request.setValue("Bearer \(token)", forHTTPHeaderField: "Authorization")
    request.setValue("application/json", forHTTPHeaderField: "content-type")
    request.timeoutInterval = 15

    let session = URLSession(configuration: .ephemeral)
    session.dataTask(with: request) { [weak self] _, response, error in
      DispatchQueue.main.async {
        if error != nil {
          self?.finish(success: false, message: "Network error — is the instance reachable?")
          return
        }
        let status = (response as? HTTPURLResponse)?.statusCode ?? 0
        switch status {
        case 201:
          self?.finish(success: true, message: "Saved")
        case 401:
          self?.finish(success: false, message: "Token rejected — reconnect in the app.")
        default:
          self?.finish(success: false, message: "Save failed (HTTP \(status)).")
        }
      }
    }.resume()
  }

  private func finish(success: Bool, message: String) {
    setState(
      title: success ? "Saved to Openmind" : "Couldn't save",
      detail: success ? nil : message,
      busy: false
    )
    titleLabel.textColor = success ? green : terracotta

    // Leave the confirmation up briefly, longer on failure so it's readable.
    DispatchQueue.main.asyncAfter(deadline: .now() + (success ? 0.9 : 2.2)) { [weak self] in
      self?.extensionContext?.completeRequest(returningItems: [], completionHandler: nil)
    }
  }

  // MARK: - UI

  private func setState(title: String, detail: String?, busy: Bool) {
    titleLabel.text = title
    detailLabel.text = detail
    detailLabel.isHidden = detail == nil
    busy ? spinner.startAnimating() : spinner.stopAnimating()
  }

  private func buildCard() {
    card.backgroundColor = paper
    card.layer.cornerRadius = 16
    card.layer.shadowColor = ink.cgColor
    card.layer.shadowOpacity = 0.18
    card.layer.shadowRadius = 24
    card.layer.shadowOffset = CGSize(width: 0, height: 8)
    card.translatesAutoresizingMaskIntoConstraints = false
    view.addSubview(card)

    titleLabel.font = .systemFont(ofSize: 17, weight: .semibold)
    titleLabel.textColor = ink
    titleLabel.textAlignment = .center

    detailLabel.font = .systemFont(ofSize: 13)
    detailLabel.textColor = inkMuted
    detailLabel.textAlignment = .center
    detailLabel.numberOfLines = 0

    spinner.color = inkMuted
    spinner.hidesWhenStopped = true

    let stack = UIStackView(arrangedSubviews: [spinner, titleLabel, detailLabel])
    stack.axis = .vertical
    stack.spacing = 8
    stack.alignment = .center
    stack.translatesAutoresizingMaskIntoConstraints = false
    card.addSubview(stack)

    NSLayoutConstraint.activate([
      card.centerXAnchor.constraint(equalTo: view.centerXAnchor),
      card.centerYAnchor.constraint(equalTo: view.centerYAnchor),
      card.widthAnchor.constraint(lessThanOrEqualTo: view.widthAnchor, constant: -48),
      card.widthAnchor.constraint(greaterThanOrEqualToConstant: 240),
      stack.topAnchor.constraint(equalTo: card.topAnchor, constant: 24),
      stack.bottomAnchor.constraint(equalTo: card.bottomAnchor, constant: -24),
      stack.leadingAnchor.constraint(equalTo: card.leadingAnchor, constant: 24),
      stack.trailingAnchor.constraint(equalTo: card.trailingAnchor, constant: -24),
    ])
  }
}
