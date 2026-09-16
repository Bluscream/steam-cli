// Native ABI adapter. Generated dispatch uses decltype(&ValveSymbol), so the
// SDK headers and host compiler, rather than a guessed FFI layout, define
// calls.
#include "steam/steam_api_flat.h"
#include "steam/steam_gameserver.h"
#if __has_include("steam/steamnetworkingfakeip.h")
#include "steam/steamnetworkingfakeip.h"
#endif
#include "json.hpp"
#include <algorithm>
#include <chrono>
#include <cmath>
#include <cstdio>
#include <cstdlib>
#include <cstring>
#include <fstream>
#include <functional>
#include <iostream>
#include <limits>
#include <map>
#include <memory>
#include <stdexcept>
#include <string>
#include <thread>
#include <type_traits>
#include <vector>
#ifdef _WIN32
#include <io.h>
#include <windows.h>
#define dup _dup
#define dup2 _dup2
#define fileno _fileno
#define fdopen _fdopen
#else
#include <dlfcn.h>
#include <unistd.h>
#endif
using json = nlohmann::json;
constexpr size_t max_buffer = 16 * 1024 * 1024, max_memory = 64 * 1024 * 1024;
struct Library {
#ifdef _WIN32
  HMODULE lib;
  Library(const char *path) {
    lib = LoadLibraryExA(path, nullptr,
                         LOAD_LIBRARY_SEARCH_DLL_LOAD_DIR |
                             LOAD_LIBRARY_SEARCH_DEFAULT_DIRS);
    if (!lib)
      throw std::runtime_error("cannot load Steam API DLL");
  }
  void *symbol(const char *name) {
    auto p = GetProcAddress(lib, name);
    if (!p)
      throw std::runtime_error(std::string("runtime lacks symbol: ") + name);
    return reinterpret_cast<void *>(p);
  }
#else
  void *lib;
  Library(const char *path) {
    lib = dlopen(path, RTLD_NOW | RTLD_LOCAL);
    if (!lib)
      throw std::runtime_error(std::string("cannot load Steam API library: ") +
                               dlerror());
  }
  void *symbol(const char *name) {
    auto p = dlsym(lib, name);
    if (!p)
      throw std::runtime_error(std::string("runtime lacks symbol: ") + name);
    return p;
  }
#endif
  template <class F> F get(const char *name) {
    return reinterpret_cast<F>(symbol(name));
  }
};
std::string hex(const void *ptr, size_t n) {
  static const char *d = "0123456789abcdef";
  std::string s(n * 2, '0');
  auto p = static_cast<const unsigned char *>(ptr);
  for (size_t i = 0; i < n; i++) {
    s[2 * i] = d[p[i] >> 4];
    s[2 * i + 1] = d[p[i] & 15];
  }
  return s;
}
std::vector<unsigned char> unhex(const std::string &s) {
  if (s.size() % 2 || s.size() > max_buffer * 2)
    throw std::runtime_error("invalid hex length");
  auto digit = [](char c) -> int {
    if (c >= '0' && c <= '9')
      return c - '0';
    if (c >= 'a' && c <= 'f')
      return c - 'a' + 10;
    if (c >= 'A' && c <= 'F')
      return c - 'A' + 10;
    throw std::runtime_error("invalid hex digit");
  };
  std::vector<unsigned char> b(s.size() / 2);
  for (size_t i = 0; i < b.size(); i++)
    b[i] = (digit(s[2 * i]) << 4) | digit(s[2 * i + 1]);
  return b;
}
struct Buffer {
  void *p;
  size_t n;
  Buffer(size_t size)
      : p(::operator new(std::max(size, size_t(1)), std::align_val_t(64))),
        n(size) {
    std::memset(p, 0, std::max(n, size_t(1)));
  }
  ~Buffer() { ::operator delete(p, std::align_val_t(64)); }
};
struct Arena {
  std::map<std::string, std::unique_ptr<Buffer>> buffers;
  std::map<std::string, void *> handles;
  size_t used = 0, serial = 0;
  std::string allocate(size_t n) {
    if (n > max_buffer || n > max_memory - used)
      throw std::runtime_error("native buffer limit exceeded");
    auto id = "b" + std::to_string(++serial);
    buffers.emplace(id, std::make_unique<Buffer>(n));
    used += n;
    return id;
  }
  std::string handle(void *p) {
    if (handles.size() >= 65536)
      throw std::runtime_error("session handle limit exceeded");
    auto id = "h" + std::to_string(++serial);
    handles[id] = p;
    return id;
  }
  Buffer &buffer(const std::string &id) {
    auto i = buffers.find(id);
    if (i == buffers.end())
      throw std::runtime_error("unknown buffer");
    return *i->second;
  }
  void *pointer(const json &j, size_t minimum) {
    if (j.contains("buffer")) {
      auto &b = buffer(j.at("buffer").get<std::string>());
      if (b.n < minimum)
        throw std::runtime_error("buffer too small for native parameter");
      return b.p;
    }
    if (j.contains("handle")) {
      auto i = handles.find(j.at("handle").get<std::string>());
      if (i == handles.end())
        throw std::runtime_error("unknown native handle");
      return i->second;
    }
    throw std::runtime_error("pointer needs a buffer or session handle");
  }
  json inspect(const std::string &id) {
    auto &b = buffer(id);
    return json{{"buffer", id}, {"size", b.n}, {"hex", hex(b.p, b.n)}};
  }
};
template <class T> T number(const json &j) {
  if constexpr (std::is_same_v<T, bool>) {
    if (!j.is_boolean())
      throw std::runtime_error("expected boolean");
    return j.get<bool>();
  } else if constexpr (std::is_enum_v<T>) {
    return static_cast<T>(number<std::underlying_type_t<T>>(j));
  } else if constexpr (std::is_integral_v<T>) {
    auto s = j.is_string() ? j.get<std::string>() : j.dump();
    size_t end = 0;
    if constexpr (std::is_signed_v<T>) {
      auto v = std::stoll(s, &end, 10);
      if (end != s.size() || v < std::numeric_limits<T>::min() ||
          v > std::numeric_limits<T>::max())
        throw std::runtime_error("integer out of range");
      return static_cast<T>(v);
    } else {
      if (s.empty() || s[0] == '-')
        throw std::runtime_error("expected unsigned integer");
      auto v = std::stoull(s, &end, 10);
      if (end != s.size() || v > std::numeric_limits<T>::max())
        throw std::runtime_error("integer out of range");
      return static_cast<T>(v);
    }
  } else {
    if (!j.is_number())
      throw std::runtime_error("expected number");
    auto v = j.get<double>();
    if (!std::isfinite(v) || std::abs(v) > std::numeric_limits<T>::max())
      throw std::runtime_error("floating point out of range");
    return static_cast<T>(v);
  }
}
template <class T, class = void> struct complete : std::false_type {};
template <class T>
struct complete<T, std::void_t<decltype(sizeof(T))>> : std::true_type {};
template <class T> T decode(const json &j, Arena &a) {
  using U = std::remove_cv_t<std::remove_reference_t<T>>;
  if constexpr (std::is_reference_v<T>) {
    auto p = decode<U *>(j, a);
    if (!p)
      throw std::runtime_error("reference cannot be null");
    return *p;
  } else if constexpr (std::is_pointer_v<T>) {
    using V = std::remove_cv_t<std::remove_pointer_t<T>>;
    if (j.is_null())
      return nullptr;
    if constexpr (std::is_function_v<V>) {
      throw std::runtime_error(
          "function callbacks require native application code; use manual "
          "callback polling where available");
    } else {
      if constexpr (std::is_same_v<T, const char *>) {
        if (j.is_string()) {
          auto s = j.get<std::string>();
          if (s.find('\0') != std::string::npos)
            throw std::runtime_error("NUL in C string");
          auto id = a.allocate(s.size() + 1);
          std::memcpy(a.buffer(id).p, s.data(), s.size());
          return static_cast<T>(a.buffer(id).p);
        }
      }
      size_t minimum = 0;
      if constexpr (complete<V>::value) {
        minimum = sizeof(V);
        if constexpr (alignof(V) > 64)
          throw std::runtime_error("over-aligned parameter unsupported");
      } else if constexpr (!std::is_void_v<V>) {
        if (j.contains("buffer"))
          throw std::runtime_error(
              "opaque SDK type requires a native handle, not a buffer");
      }
      return static_cast<T>(a.pointer(j, minimum));
    }
  } else if constexpr (std::is_arithmetic_v<U> || std::is_enum_v<U>) {
    return number<U>(j);
  } else if constexpr (std::is_trivially_copyable_v<U> &&
                       std::is_default_constructible_v<U>) {
    auto b = unhex(j.at("hex").get<std::string>());
    if (b.size() != sizeof(U))
      throw std::runtime_error("struct hex must match native sizeof(type)");
    U value{};
    std::memcpy(&value, b.data(), sizeof(U));
    return value;
  } else {
    throw std::runtime_error(
        "nontrivial native value requires application-specific binding");
  }
}
template <class T> json encode(T value, Arena &a) {
  using U = std::remove_cv_t<T>;
  if constexpr (std::is_same_v<U, const char *>) {
    if (!value)
      return nullptr;
    size_t n = 0;
    while (n < max_buffer && value[n])
      n++;
    if (n == max_buffer)
      throw std::runtime_error("native string too long");
    return std::string(value, n);
  } else if constexpr (std::is_pointer_v<U>) {
    if (!value)
      return nullptr;
    return json{{"handle", a.handle(const_cast<void *>(
                               static_cast<const void *>(value)))}};
  } else if constexpr (std::is_enum_v<U>) {
    return encode(static_cast<std::underlying_type_t<U>>(value), a);
  } else if constexpr (std::is_integral_v<U> && sizeof(U) == 8) {
    return std::to_string(value);
  } else if constexpr (std::is_arithmetic_v<U>) {
    return value;
  } else if constexpr (std::is_trivially_copyable_v<U>) {
    return json{{"hex", hex(&value, sizeof(U))}, {"size", sizeof(U)}};
  } else {
    throw std::runtime_error("unsupported native return value");
  }
}
template <class R, class... A, size_t... I>
json invokeImpl(R (*fn)(A...), const json &args, Arena &a,
                std::index_sequence<I...>) {
  if constexpr (std::is_void_v<R>) {
    fn(decode<A>(args[I], a)...);
    return nullptr;
  } else {
    return encode(fn(decode<A>(args[I], a)...), a);
  }
}
template <class R, class... A>
json invoke(R (*fn)(A...), const json &args, Arena &a) {
  if (!args.is_array() || args.size() != sizeof...(A))
    throw std::runtime_error("wrong native argument count");
  return invokeImpl(fn, args, a, std::index_sequence_for<A...>{});
}
using Call = std::function<json(Library &, Arena &, const json &)>;
struct Method {
  std::string accessor, serverAccessor;
  Call call;
};
std::map<std::string, Method> methods;
std::map<int, std::string> callbacks;
std::map<std::string, size_t> sizes;
std::map<std::string, json> layouts;
template <class T> struct TypeTag {
  using type = T;
};
template <class T> void layout(const std::string &name) {
  if constexpr (complete<T>::value) {
    sizes[name] = sizeof(T);
    layouts[name] = {{"size", sizeof(T)},
                     {"alignment", alignof(T)},
                     {"fields", json::object()}};
  }
}
template <class T> void callback(const std::string &name) {
  if constexpr (complete<T>::value)
    callbacks[T::k_iCallback] = name;
}
void register_methods();
struct Session {
  Library lib;
  Arena arena;
  bool initialized = false, server = false;
  explicit Session(const char *path) : lib(path) {}
  ~Session() {
    if (initialized) {
      try {
        lib.get<void (*)()>(server ? "SteamGameServer_Shutdown"
                                   : "SteamAPI_Shutdown")();
      } catch (...) {
      }
    }
  }
  json request(const json &r) {
    std::string op = r.at("op");
    if (op == "init") {
      if (initialized)
        throw std::runtime_error("session already initialized");
      SteamErrMsg error{};
      int result;
      auto mode = r.value("mode", std::string("client"));
      if (mode != "client" && mode != "gameserver")
        throw std::runtime_error("mode must be client or gameserver");
      server = mode == "gameserver";
      if (server) {
        result = lib.get<decltype(&SteamInternal_GameServer_Init_V2)>(
            "SteamInternal_GameServer_Init_V2")(
            number<uint32>(r.value("ip", json(0))),
            number<uint16>(r.value("game_port", json(27015))),
            number<uint16>(r.value("query_port", json(27016))),
            number<EServerMode>(r.value("server_mode", json(1))),
            r.value("version", std::string("1.0.0.0")).c_str(), "", &error);
      } else
        result =
            lib.get<decltype(&SteamAPI_InitFlat)>("SteamAPI_InitFlat")(&error);
      if (result != 0)
        throw std::runtime_error(std::string("Steam initialization failed (") +
                                 std::to_string(result) + "): " + error);
      initialized = true;
      lib.get<decltype(&SteamAPI_ManualDispatch_Init)>(
          "SteamAPI_ManualDispatch_Init")();
      return json{{"initialized", true},
                  {"mode", server ? "gameserver" : "client"}};
    }
    if (op == "layout") {
      auto name = r.at("type").get<std::string>();
      auto i = sizes.find(name);
      if (i == sizes.end())
        throw std::runtime_error("type has no compiled layout");
      auto result = layouts[name];
      result["type"] = name;
      return result;
    }
    if (op == "buffer") {
      std::vector<unsigned char> b;
      if (r.contains("hex"))
        b = unhex(r.at("hex"));
      size_t n = r.contains("size") ? number<size_t>(r.at("size")) : b.size();
      if (b.size() > n)
        throw std::runtime_error("hex exceeds buffer size");
      auto id = arena.allocate(n);
      if (!b.empty())
        std::memcpy(arena.buffer(id).p, b.data(), b.size());
      return arena.inspect(id);
    }
    if (op == "read") {
      return arena.inspect(r.at("buffer").get<std::string>());
    }
    if (op == "write") {
      auto &b = arena.buffer(r.at("buffer").get<std::string>());
      auto data = unhex(r.at("hex"));
      if (data.size() > b.n)
        throw std::runtime_error("write exceeds buffer");
      std::memcpy(b.p, data.data(), data.size());
      return arena.inspect(r.at("buffer"));
    }
    if (!initialized)
      throw std::runtime_error(
          "initialize Steam before calling methods or polling callbacks");
    if (op == "call") {
      auto name = r.at("method").get<std::string>();
      auto i = methods.find(name);
      if (i == methods.end())
        throw std::runtime_error("unknown SDK method");
      json args = r.value("args", json::array());
      if (!args.is_array())
        throw std::runtime_error("args must be a JSON array");
      json self;
      if (r.contains("self"))
        self = r.at("self");
      else {
        auto accessor = server && !i->second.serverAccessor.empty()
                            ? i->second.serverAccessor
                            : i->second.accessor;
        if (accessor.empty())
          throw std::runtime_error(
              "method requires an explicit self buffer/handle");
        auto p = accessor == "@client"
                     ? lib.get<void *(*)(const char *)>(
                           "SteamInternal_CreateInterface")(
                           STEAMCLIENT_INTERFACE_VERSION)
                     : lib.get<void *(*)()>(accessor.c_str())();
        if (!p)
          throw std::runtime_error(
              "Steam interface unavailable in this session");
        self = json{{"handle", arena.handle(p)}};
      }
      if (self.is_null())
        throw std::runtime_error("self cannot be null");
      args.insert(args.begin(), self);
      auto value = i->second.call(lib, arena, args);
      return json{{"value", value}};
    }
    if (op == "poll") {
      int ms = number<int>(r.value("wait_ms", json(0)));
      if (ms < 0 || ms > 60000)
        throw std::runtime_error("wait_ms must be 0..60000");
      auto pipe =
          lib.get<HSteamPipe (*)()>(server ? "SteamGameServer_GetHSteamPipe"
                                           : "SteamAPI_GetHSteamPipe")();
      auto frame = lib.get<decltype(&SteamAPI_ManualDispatch_RunFrame)>(
          "SteamAPI_ManualDispatch_RunFrame");
      auto next = lib.get<decltype(&SteamAPI_ManualDispatch_GetNextCallback)>(
          "SteamAPI_ManualDispatch_GetNextCallback");
      auto freeLast =
          lib.get<decltype(&SteamAPI_ManualDispatch_FreeLastCallback)>(
              "SteamAPI_ManualDispatch_FreeLastCallback");
      json events = json::array();
      auto until =
          std::chrono::steady_clock::now() + std::chrono::milliseconds(ms);
      size_t bytes = 0;
      do {
        frame(pipe);
        CallbackMsg_t msg{};
        while (events.size() < 1024 && next(pipe, &msg)) {
          try {
            if (msg.m_cubParam < 0 || size_t(msg.m_cubParam) > max_buffer ||
                bytes + msg.m_cubParam > max_buffer)
              throw std::runtime_error("callback data exceeds limit");
            bytes += msg.m_cubParam;
            json event = {{"callback", msg.m_iCallback},
                          {"name", callbacks[msg.m_iCallback]},
                          {"hex", hex(msg.m_pubParam, msg.m_cubParam)}};
            if (msg.m_iCallback == SteamAPICallCompleted_t::k_iCallback &&
                msg.m_cubParam == sizeof(SteamAPICallCompleted_t)) {
              SteamAPICallCompleted_t done{};
              std::memcpy(&done, msg.m_pubParam, sizeof(done));
              if (done.m_cubParam > max_buffer - bytes)
                throw std::runtime_error("call result exceeds limit");
              std::vector<unsigned char> data(done.m_cubParam);
              bool failed = false;
              bool ok =
                  lib.get<decltype(&SteamAPI_ManualDispatch_GetAPICallResult)>(
                      "SteamAPI_ManualDispatch_GetAPICallResult")(
                      pipe, done.m_hAsyncCall, data.data(), data.size(),
                      done.m_iCallback, &failed);
              event["call"] = std::to_string(done.m_hAsyncCall);
              event["result_callback"] = done.m_iCallback;
              event["result_name"] = callbacks[done.m_iCallback];
              event["failed"] = failed || !ok;
              if (ok) {
                event["result_hex"] = hex(data.data(), data.size());
                bytes += data.size();
              }
            }
            events.push_back(event);
            freeLast(pipe);
          } catch (...) {
            freeLast(pipe);
            throw;
          }
        }
        if (!events.empty() || ms == 0)
          break;
        std::this_thread::sleep_for(std::chrono::milliseconds(10));
      } while (std::chrono::steady_clock::now() < until);
      return events;
    }
    throw std::runtime_error("unknown native operation");
  }
};
int main(int argc, char **argv) {
  FILE *protocol = fdopen(dup(fileno(stdout)), "w");
  dup2(fileno(stderr), fileno(stdout));
  auto emit = [&](const json &j) {
    auto s = j.dump(-1, ' ', false, json::error_handler_t::replace);
    std::fprintf(protocol, "%s\n", s.c_str());
    std::fflush(protocol);
  };
  try {
    if (argc != 2 && argc != 5)
      throw std::runtime_error("pass an absolute Steam runtime library path");
    register_methods();
    Session session(argv[1]);
    if (argc == 5) {
      if (std::string(argv[2]) != "--idle")
        throw std::runtime_error("unknown helper mode");
      auto result = session.request(json{{"op", "init"}});
      emit({{"ok", true}, {"result", result}});
      for (;;) {
        std::ifstream control(argv[3]);
        std::string token;
        std::getline(control, token);
        if (!control || token != argv[4])
          break;
        session.request(json{{"op", "poll"}});
        std::this_thread::sleep_for(std::chrono::milliseconds(250));
      }
      return 0;
    }
    bool failed = false;
    std::string line;
    while (true) {
      line.clear();
      char ch;
      while (std::cin.get(ch) && ch != '\n') {
        line.push_back(ch);
        if (line.size() > max_buffer)
          break;
      }
      if (line.empty() && !std::cin)
        break;
      if (line.size() > max_buffer) {
        emit({{"ok", false}, {"error", "request exceeds limit"}});
        return 1;
      }
      json request;
      try {
        request = json::parse(line);
        auto value = session.request(request);
        json reply = {{"ok", true}, {"result", value}};
        if (request.contains("id"))
          reply["id"] = request["id"];
        emit(reply);
      } catch (const std::exception &e) {
        failed = true;
        json reply = {{"ok", false}, {"error", e.what()}};
        if (request.is_object() && request.contains("id"))
          reply["id"] = request["id"];
        emit(reply);
      }
    }
    return failed ? 1 : 0;
  } catch (const std::exception &e) {
    emit({{"ok", false}, {"error", e.what()}});
    return 1;
  }
}
// GENERATED_DISPATCH
