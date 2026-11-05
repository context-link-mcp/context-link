#include "sample.hpp"

// In-class definitions keep their class prefix from the enclosing class.
class Counter {
public:
    Counter() {}
    ~Counter() {}
    int get() { return value; }
    bool operator==(const Counter& other) const { return value == other.value; }
    int* raw() { return &value; }
    void reset();
    int bump();

private:
    int value = 0;
};

// Out-of-class definitions take the prefix from the qualified name's scope.
void Counter::reset() { value = 0; }

int Counter::bump() {
    reset();
    this->get();
    return Counter::get() + helper();
}

Counter& Counter::self() { return *this; }

int* Counter::ptr() { return &value; }

Box::Box() {}

Box::~Box() {}

bool Box::operator<(const Box& other) const { return size < other.size; }

// Namespace-qualified: only the last scope segment is kept.
int ns::Store::load() { return 1; }

int Outer::Inner::depth() { return 2; }

template <typename T>
T Holder<T>::value() { return item; }

namespace app {
int Service::start() { return 0; }
}

// A scoped namespace function is named like a method of its namespace.
int util::clamp(int x) { return x; }

int helper() { return 3; }
